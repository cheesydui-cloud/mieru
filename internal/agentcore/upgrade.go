package agentcore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// upgradeJob matches panel heartbeat payload.
type upgradeJob struct {
	ID      string   `json:"id"`
	Version string   `json:"version"`
	URLs    []string `json:"urls"`
	Asset   string   `json:"asset"`
}

// scheduleUpgrade runs self-upgrade once in the background.
func (a *Agent) scheduleUpgrade(ctx context.Context, job upgradeJob) {
	if job.ID == "" || job.Version == "" {
		return
	}
	// Same job re-delivered each heartbeat until panel sees new version — run once.
	a.stateMu.Lock()
	if a.lastUpgradeJobID == job.ID {
		a.stateMu.Unlock()
		return
	}
	a.stateMu.Unlock()
	// Avoid concurrent upgrades.
	if !atomic.CompareAndSwapInt32(&a.upgradeBusy, 0, 1) {
		log.Printf("upgrade: already in progress, skip job=%s", job.ID)
		return
	}
	a.stateMu.Lock()
	a.lastUpgradeJobID = job.ID
	a.stateMu.Unlock()
	go func() {
		defer atomic.StoreInt32(&a.upgradeBusy, 0)
		// Detach from heartbeat cancel; allow up to 5 min download.
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ver, err := a.doSelfUpgrade(uctx, job)
		if err != nil {
			log.Printf("upgrade FAILED job=%s: %v", job.ID, err)
			_ = a.postJSON(context.Background(), "/api/agent/upgrade-result", map[string]interface{}{
				"node_id": a.cfg.NodeID,
				"token":   a.cfg.Token,
				"job_id":  job.ID,
				"ok":      false,
				"error":   err.Error(),
			}, nil)
			return
		}
		log.Printf("upgrade OK job=%s version=%s — reporting then restarting", job.ID, ver)
		_ = a.postJSON(context.Background(), "/api/agent/upgrade-result", map[string]interface{}{
			"node_id": a.cfg.NodeID,
			"token":   a.cfg.Token,
			"job_id":  job.ID,
			"ok":      true,
			"version": ver,
		}, nil)
		// Prefer systemd restart so unit stays managed; else exec self.
		a.restartAfterUpgrade()
	}()
}

func (a *Agent) doSelfUpgrade(ctx context.Context, job upgradeJob) (string, error) {
	want := strings.TrimSpace(job.Version)
	if want == "" {
		return "", fmt.Errorf("empty version")
	}
	// Already on target?
	cur := strings.TrimPrefix(AgentVersion, "v")
	wantBare := strings.TrimPrefix(want, "v")
	if cur == wantBare {
		return want, nil
	}
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("self-upgrade only supported on linux (got %s)", runtime.GOOS)
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported arch %s", arch)
	}
	asset := fmt.Sprintf("mieru-panel-%s-linux-%s.tar.gz", want, arch)
	if !strings.HasPrefix(want, "v") {
		asset = fmt.Sprintf("mieru-panel-v%s-linux-%s.tar.gz", want, arch)
		want = "v" + want
	}

	// Filter panel URLs to this arch; if empty, rebuild mirror list.
	urls := filterURLsForAsset(job.URLs, asset)
	if len(urls) == 0 {
		urls = agentPackageURLs(want, asset)
	}

	tmpDir, err := os.MkdirTemp("", "mieru-agent-upgrade-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	tgz := filepath.Join(tmpDir, asset)

	if err := downloadFirstURL(ctx, urls, tgz); err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	st, err := os.Stat(tgz)
	if err != nil || st.Size() < 1_000_000 {
		sz := int64(0)
		if st != nil {
			sz = st.Size()
		}
		return "", fmt.Errorf("tarball too small (%d bytes)", sz)
	}

	agentBin, err := extractNamedBinary(tgz, "agent", tmpDir)
	if err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}
	// Verify -version
	out, err := exec.Command(agentBin, "-version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("new binary -version: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	got := strings.TrimSpace(string(out))
	gotBare := strings.TrimPrefix(got, "v")
	if gotBare != wantBare && got != want {
		return "", fmt.Errorf("new binary version %q != want %q", got, want)
	}

	// Install to standard paths (same as install-agent.sh).
	targets := []string{
		"/usr/local/bin/mieru-agent",
		"/opt/mieru-agent/agent",
	}
	// Also replace current executable if different.
	if self, err := os.Executable(); err == nil {
		if real, err2 := filepath.EvalSymlinks(self); err2 == nil {
			self = real
		}
		dup := false
		for _, t := range targets {
			if t == self {
				dup = true
				break
			}
		}
		if !dup {
			targets = append(targets, self)
		}
	}

	data, err := os.ReadFile(agentBin)
	if err != nil {
		return "", err
	}
	installed := 0
	var lastErr error
	for _, dest := range targets {
		if err := installBinaryBytes(dest, data); err != nil {
			lastErr = err
			log.Printf("upgrade: install %s: %v", dest, err)
			continue
		}
		installed++
		log.Printf("upgrade: installed %s", dest)
	}
	if installed == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no install target")
		}
		return "", lastErr
	}
	return got, nil
}

func filterURLsForAsset(urls []string, asset string) []string {
	out := make([]string, 0, len(urls))
	for _, u := range urls {
		if strings.Contains(u, asset) {
			out = append(out, u)
		}
	}
	return out
}

func agentPackageURLs(ver, asset string) []string {
	repo := "cheesydui-cloud/mieru"
	base := fmt.Sprintf("%s/releases/download/%s/%s", repo, ver, asset)
	return []string{
		"https://github.com/" + base,
		"https://ghfast.top/https://github.com/" + base,
		"https://mirror.ghproxy.com/https://github.com/" + base,
		"https://ghproxy.net/https://github.com/" + base,
		"https://gitdl.cn/https://github.com/" + base,
	}
}

func downloadFirstURL(ctx context.Context, urls []string, dest string) error {
	client := &http.Client{Timeout: 3 * time.Minute}
	var last error
	for _, u := range urls {
		log.Printf("upgrade: download %s", u)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			last = err
			continue
		}
		res, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		func() {
			defer res.Body.Close()
			if res.StatusCode != 200 {
				last = fmt.Errorf("HTTP %d for %s", res.StatusCode, u)
				return
			}
			f, err := os.Create(dest)
			if err != nil {
				last = err
				return
			}
			defer f.Close()
			n, err := io.Copy(f, res.Body)
			if err != nil {
				last = err
				return
			}
			if n < 1_000_000 {
				last = fmt.Errorf("file too small %d from %s", n, u)
				_ = os.Remove(dest)
				return
			}
			last = nil
		}()
		if last == nil {
			return nil
		}
	}
	if last == nil {
		last = fmt.Errorf("no urls")
	}
	return last
}

func extractNamedBinary(tgz, name, outDir string) (string, error) {
	f, err := os.Open(tgz)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != name {
			continue
		}
		// Prefer larger "agent" (skip junk)
		dest := filepath.Join(outDir, name+".new")
		w, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		n, err := io.Copy(w, tr)
		_ = w.Close()
		if err != nil {
			return "", err
		}
		if n < 1_000_000 {
			_ = os.Remove(dest)
			continue
		}
		return dest, nil
	}
	return "", fmt.Errorf("%s not found in tarball", name)
}

func installBinaryBytes(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := dest + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	// Atomic replace when possible.
	if err := os.Rename(tmp, dest); err != nil {
		// Cross-device or busy: copy over
		if err2 := os.WriteFile(dest, data, 0o755); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
		_ = os.Remove(tmp)
	}
	_ = os.Chmod(dest, 0o755)
	return nil
}

func (a *Agent) restartAfterUpgrade() {
	// systemctl restart if unit exists
	if path, err := exec.LookPath("systemctl"); err == nil {
		cmd := exec.Command(path, "restart", "mieru-agent")
		if out, err := cmd.CombinedOutput(); err == nil {
			log.Printf("upgrade: systemctl restart mieru-agent OK")
			// Process will be killed by systemd shortly.
			time.Sleep(2 * time.Second)
			os.Exit(0)
		} else {
			log.Printf("upgrade: systemctl restart: %v (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	// Fallback: exec new binary in place of current process.
	self, err := os.Executable()
	if err != nil {
		log.Printf("upgrade: no executable path, exit for supervisor restart")
		os.Exit(0)
	}
	if real, err2 := filepath.EvalSymlinks(self); err2 == nil {
		self = real
	}
	log.Printf("upgrade: exec %s", self)
	// Best-effort: start detached then exit so systemd/supervisor can take over if exec fails.
	cmd := exec.Command(self)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("upgrade: start new process: %v — exit 0 for Restart=always", err)
	}
	os.Exit(0)
}
