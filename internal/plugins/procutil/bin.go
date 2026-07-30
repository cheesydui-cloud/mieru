package procutil

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Default mieru/mita release used when binaries are missing.
const DefaultMieruVersion = "3.35.0"

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
	asset := fmt.Sprintf("%s_%s_%s_%s.tar.gz", name, ver, goos, arch)
	url := fmt.Sprintf("https://github.com/enfein/mieru/releases/download/v%s/%s", ver, asset)
	log.Printf("[procutil] downloading %s → %s", url, dest)
	if err := downloadAndExtractBinary(url, name, dest); err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	return dest, nil
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
