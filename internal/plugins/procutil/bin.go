package procutil

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Default mieru/mita release used when binaries are missing.
const DefaultMieruVersion = "3.35.0"

var (
	daemonMu    sync.Mutex
	mitaDaemons = map[string]*exec.Cmd{} // uds path → managed `mita run`
)

// LookPath finds bin in PATH or common install dirs.
func LookPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, d := range []string{"/usr/local/bin", "/usr/bin", "/opt/mieru/bin"} {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// EnsureBinary returns path to name (mieru or mita), downloading into binDir if missing.
// Prefers already-installed system binary (deb/rpm package), then binDir copy.
func EnsureBinary(name, binDir string) (string, error) {
	if p := LookPath(name); p != "" {
		return p, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(binDir, name)
	if st, err := os.Stat(dest); err == nil && !st.IsDir() {
		return dest, nil
	}
	ver := os.Getenv("MIERU_BIN_VERSION")
	if ver == "" {
		ver = DefaultMieruVersion
	}
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	if goos != "linux" {
		return "", fmt.Errorf("%s binary auto-install only supported on linux (have %s); install %s manually", name, goos, name)
	}
	arch := goarch
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported arch %s for %s", goarch, name)
	}

	// Prefer official packages for mita (ships systemd unit with `mita run`) — OneClick path.
	if name == "mita" {
		if p, err := tryInstallMitaPackage(ver, arch); err == nil && p != "" {
			return p, nil
		} else if err != nil {
			log.Printf("[procutil] mita package install skipped: %v — falling back to tarball", err)
		}
	}

	asset := fmt.Sprintf("%s_%s_%s_%s.tar.gz", name, ver, goos, arch)
	// Multiple mirrors — CN hosts often cannot reach github.com directly.
	urls := releaseURLs(ver, asset)
	var lastErr error
	for _, url := range urls {
		log.Printf("[procutil] downloading %s → %s", url, dest)
		if err := downloadAndExtractBinary(url, name, dest); err != nil {
			log.Printf("[procutil] download failed: %v", err)
			lastErr = err
			continue
		}
		return dest, nil
	}
	return "", fmt.Errorf("download %s: all mirrors failed (last: %w); install manually: put %s in PATH or %s", name, lastErr, name, dest)
}

// releaseURLs returns candidate download URLs (GitHub + common CN-friendly mirrors).
func releaseURLs(ver, asset string) []string {
	base := fmt.Sprintf("enfein/mieru/releases/download/v%s/%s", ver, asset)
	urls := []string{
		"https://github.com/" + base,
		// ghproxy-style mirrors (may rotate)
		"https://ghfast.top/https://github.com/" + base,
		"https://mirror.ghproxy.com/https://github.com/" + base,
		"https://ghproxy.net/https://github.com/" + base,
		"https://gitdl.cn/https://github.com/" + base,
	}
	// Allow operator override: MIERU_DOWNLOAD_MIRROR=https://my.cdn/prefix
	if m := strings.TrimRight(os.Getenv("MIERU_DOWNLOAD_MIRROR"), "/"); m != "" {
		urls = append([]string{m + "/https://github.com/" + base, m + "/" + base}, urls...)
	}
	return urls
}

func tryInstallMitaPackage(ver, arch string) (string, error) {
	// Only attempt when we can use package managers as root.
	if os.Geteuid() != 0 {
		return "", fmt.Errorf("not root")
	}
	var pkgPath string
	tmp := os.TempDir()
	switch {
	case fileExists("/usr/bin/dpkg") || fileExists("/usr/bin/apt-get"):
		debArch := arch
		if arch == "amd64" {
			debArch = "amd64"
		}
		if arch == "arm64" {
			debArch = "arm64"
		}
		asset := fmt.Sprintf("mita_%s_%s.deb", ver, debArch)
		pkgPath = filepath.Join(tmp, asset)
		if err := downloadFirst(releaseURLs(ver, asset), pkgPath); err != nil {
			return "", err
		}
		cmd := exec.Command("dpkg", "-i", pkgPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			// try apt fix
			_ = exec.Command("apt-get", "install", "-f", "-y").Run()
			cmd2 := exec.Command("dpkg", "-i", pkgPath)
			out2, err2 := cmd2.CombinedOutput()
			if err2 != nil {
				return "", fmt.Errorf("dpkg: %v (%s %s)", err2, strings.TrimSpace(string(out)), strings.TrimSpace(string(out2)))
			}
		}
		log.Printf("[procutil] installed mita deb package")
	case fileExists("/usr/bin/rpm") || fileExists("/usr/bin/dnf") || fileExists("/usr/bin/yum"):
		rpmArch := arch
		if arch == "amd64" {
			rpmArch = "x86_64"
		}
		if arch == "arm64" {
			rpmArch = "aarch64"
		}
		asset := fmt.Sprintf("mita-%s-1.%s.rpm", ver, rpmArch)
		pkgPath = filepath.Join(tmp, asset)
		if err := downloadFirst(releaseURLs(ver, asset), pkgPath); err != nil {
			return "", err
		}
		var cmd *exec.Cmd
		if LookPath("dnf") != "" {
			cmd = exec.Command("dnf", "install", "-y", pkgPath)
		} else if LookPath("yum") != "" {
			cmd = exec.Command("yum", "install", "-y", pkgPath)
		} else {
			cmd = exec.Command("rpm", "-Uvh", "--force", pkgPath)
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("rpm install: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		log.Printf("[procutil] installed mita rpm package")
	default:
		return "", fmt.Errorf("no dpkg/rpm")
	}
	_ = os.Remove(pkgPath)
	// package places binary at /usr/bin/mita or /usr/local/bin/mita
	if p := LookPath("mita"); p != "" {
		// enable systemd unit from package (ExecStart=mita run)
		_, _ = RunCapture("systemctl", "daemon-reload")
		_, _ = RunCapture("systemctl", "enable", "mita")
		_, _ = RunCapture("systemctl", "start", "mita")
		return p, nil
	}
	return "", fmt.Errorf("mita not found after package install")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 3 * time.Minute}
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", res.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, res.Body)
	return err
}

func downloadFirst(urls []string, dest string) error {
	var last error
	for _, u := range urls {
		log.Printf("[procutil] trying package %s", u)
		if err := downloadFile(u, dest); err != nil {
			last = err
			continue
		}
		return nil
	}
	if last == nil {
		last = fmt.Errorf("no urls")
	}
	return last
}

func downloadAndExtractBinary(url, binName, dest string) error {
	client := &http.Client{Timeout: 3 * time.Minute}
	res, err := client.Get(url)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", res.StatusCode, url)
	}
	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var found bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if base != binName && !strings.HasSuffix(base, binName) {
			continue
		}
		tmp := dest + ".tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			_ = os.Remove(tmp)
			return err
		}
		f.Close()
		if err := os.Rename(tmp, dest); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		found = true
		break
	}
	if !found {
		return fmt.Errorf("binary %s not found in archive", binName)
	}
	return nil
}

// RunCapture runs cmd and returns combined output.
func RunCapture(bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunCaptureEnv runs cmd with extra env vars (KEY=VAL).
func RunCaptureEnv(env []string, bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// MitaRuntime describes how to talk to a mita management daemon.
type MitaRuntime struct {
	Bin     string
	Env     []string // MITA_UDS_PATH etc for CLI + daemon
	UDSPath string
	Managed bool // we started `mita run` ourselves
}

// System mita socket paths (package install).
func systemMitaSockets() []string {
	return []string{
		"/var/run/mita/mita.sock",
		"/run/mita/mita.sock",
		"/var/run/mita.sock",
	}
}

func socketReady(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	// unix socket: ModeSocket
	return st.Mode()&os.ModeSocket != 0
}

func anySystemSocketReady() string {
	for _, s := range systemMitaSockets() {
		if socketReady(s) {
			return s
		}
	}
	return ""
}

// EnsureMitaDaemon makes sure the mita management process is up (like OneClick ensure_mita_daemon).
// Official model: systemd runs `mita run`; CLI uses UDS to apply/start proxy.
// If package/systemd is unavailable, we spawn `mita run` with a private UDS under dataDir.
func EnsureMitaDaemon(bin, dataDir string) (*MitaRuntime, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}

	// 1) Prefer system package service (OneClick / official deb-rpm).
	if LookPath("systemctl") != "" {
		// only if unit exists
		if out, err := RunCapture("systemctl", "list-unit-files", "mita.service"); err == nil && strings.Contains(out, "mita.service") {
			_, _ = RunCapture("systemctl", "enable", "mita")
			if _, err := RunCapture("systemctl", "start", "mita"); err != nil {
				_, _ = RunCapture("systemctl", "restart", "mita")
			}
			if waitSocketAny(systemMitaSockets(), 20*time.Second) {
				sock := anySystemSocketReady()
				log.Printf("[procutil] mita system daemon ready sock=%s", sock)
				// Always set MITA_UDS_PATH so CLI talks to the same socket the
				// daemon uses (non-default paths / multi-instance safe).
				env := []string{
					"MITA_UDS_PATH=" + sock,
					"MITA_INSECURE_UDS=1",
				}
				return &MitaRuntime{Bin: bin, Env: env, UDSPath: sock, Managed: false}, nil
			}
			log.Printf("[procutil] system mita.service present but socket not ready — trying managed run")
		}
	}

	// 2) Managed daemon under our data dir (tar binary path).
	// Official package uses /var/run/mita + /var/lib/mita; when we manage the daemon
	// ourselves (tarball), keep everything under dataDir so no mita system user is required.
	uds := filepath.Join(dataDir, "mita.sock")
	libDir := filepath.Join(dataDir, "lib")
	_ = os.MkdirAll(libDir, 0o755)
	_ = os.MkdirAll(filepath.Join(dataDir, "run"), 0o755)
	// root helpers for package-like layout if present
	if os.Geteuid() == 0 {
		_ = os.MkdirAll("/var/lib/mita", 0o755)
		_ = os.MkdirAll("/var/run/mita", 0o755)
		_ = os.MkdirAll("/etc/mita", 0o755)
	}
	env := []string{
		"MITA_UDS_PATH=" + uds,
		"MITA_INSECURE_UDS=1",
		// keep metrics writable without dedicated mita user
		"HOME=" + dataDir,
	}

	if socketReady(uds) {
		// probe status
		if out, err := RunCaptureEnv(env, bin, "status"); err == nil || strings.Contains(out, "status") {
			return &MitaRuntime{Bin: bin, Env: env, UDSPath: uds, Managed: true}, nil
		}
		// stale socket
		_ = os.Remove(uds)
	}

	daemonMu.Lock()
	defer daemonMu.Unlock()

	if cmd, ok := mitaDaemons[uds]; ok && cmd.Process != nil {
		// still running?
		if cmd.ProcessState == nil {
			if waitSocket([]string{uds}, 5*time.Second) {
				return &MitaRuntime{Bin: bin, Env: env, UDSPath: uds, Managed: true}, nil
			}
		}
		delete(mitaDaemons, uds)
	}

	logFile := filepath.Join(dataDir, "mita-run.log")
	lf, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bin, "run")
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = lf
	cmd.Stderr = lf
	// detach from agent lifecycle partially — if agent restarts, systemd restarts agent and we re-spawn
	if err := cmd.Start(); err != nil {
		lf.Close()
		return nil, fmt.Errorf("start mita run: %w", err)
	}
	mitaDaemons[uds] = cmd
	go func() {
		_ = cmd.Wait()
		lf.Close()
		daemonMu.Lock()
		delete(mitaDaemons, uds)
		daemonMu.Unlock()
		log.Printf("[procutil] mita run exited")
	}()

	if !waitSocket([]string{uds}, 30*time.Second) {
		return nil, fmt.Errorf("mita run started but UDS %s not ready (see %s)", uds, logFile)
	}
	log.Printf("[procutil] managed mita run ready uds=%s pid=%d", uds, cmd.Process.Pid)
	return &MitaRuntime{Bin: bin, Env: env, UDSPath: uds, Managed: true}, nil
}

func waitSocketAny(paths []string, d time.Duration) bool {
	return waitSocket(paths, d)
}

func waitSocket(paths []string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		for _, p := range paths {
			if socketReady(p) {
				// extra: try dial
				c, err := net.DialTimeout("unix", p, 500*time.Millisecond)
				if err == nil {
					c.Close()
					return true
				}
				// some systems socket exists but not accepting yet
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// MitaCmd runs mita CLI against the runtime (with UDS env if managed).
func (rt *MitaRuntime) MitaCmd(args ...string) (string, error) {
	if rt == nil {
		return "", fmt.Errorf("nil mita runtime")
	}
	if len(rt.Env) > 0 {
		return RunCaptureEnv(rt.Env, rt.Bin, args...)
	}
	return RunCapture(rt.Bin, args...)
}
