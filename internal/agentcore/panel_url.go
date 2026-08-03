package agentcore

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// panelURLJob matches panel heartbeat payload.
type panelURLJob struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// Default env file written by install-agent.sh.
const defaultAgentEnvFile = "/etc/mieru-agent.env"

// schedulePanelURLUpdate rewrites AGENT_PANEL_URL in env file and restarts agent.
func (a *Agent) schedulePanelURLUpdate(ctx context.Context, job panelURLJob) {
	if job.ID == "" {
		return
	}
	url := normalizeAgentPanelURL(job.URL)
	if url == "" {
		log.Printf("panel-url: empty url job=%s", job.ID)
		return
	}
	// Same job re-delivered each heartbeat until panel sees success — run once.
	a.stateMu.Lock()
	if a.lastPanelURLJobID == job.ID {
		a.stateMu.Unlock()
		return
	}
	a.stateMu.Unlock()
	if !atomic.CompareAndSwapInt32(&a.panelURLBusy, 0, 1) {
		log.Printf("panel-url: already in progress, skip job=%s", job.ID)
		return
	}
	a.stateMu.Lock()
	a.lastPanelURLJobID = job.ID
	a.stateMu.Unlock()
	go func() {
		defer atomic.StoreInt32(&a.panelURLBusy, 0)
		// Detach from heartbeat cancel.
		uctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		applied, err := a.doPanelURLUpdate(uctx, url)
		if err != nil {
			log.Printf("panel-url FAILED job=%s: %v", job.ID, err)
			_ = a.postJSON(context.Background(), "/api/agent/panel-url-result", map[string]interface{}{
				"node_id": a.cfg.NodeID,
				"token":   a.cfg.Token,
				"job_id":  job.ID,
				"ok":      false,
				"error":   err.Error(),
			}, nil)
			// Allow retry on a later delivery of the same or new job.
			a.stateMu.Lock()
			if a.lastPanelURLJobID == job.ID {
				a.lastPanelURLJobID = ""
			}
			a.stateMu.Unlock()
			return
		}
		if !applied {
			// Already on target — report ok, no restart.
			log.Printf("panel-url job=%s already on %s", job.ID, url)
			_ = a.postJSON(context.Background(), "/api/agent/panel-url-result", map[string]interface{}{
				"node_id": a.cfg.NodeID,
				"token":   a.cfg.Token,
				"job_id":  job.ID,
				"ok":      true,
				"url":     url,
			}, nil)
			return
		}
		log.Printf("panel-url OK job=%s url=%s — reporting then restarting", job.ID, url)
		_ = a.postJSON(context.Background(), "/api/agent/panel-url-result", map[string]interface{}{
			"node_id": a.cfg.NodeID,
			"token":   a.cfg.Token,
			"job_id":  job.ID,
			"ok":      true,
			"url":     url,
		}, nil)
		// Update in-memory config before restart (best-effort if restart fails).
		a.cfg.PanelURL = url
		a.restartAfterUpgrade()
	}()
}

// doPanelURLUpdate rewrites env file. Returns applied=true when file changed.
func (a *Agent) doPanelURLUpdate(ctx context.Context, want string) (applied bool, err error) {
	_ = ctx
	want = normalizeAgentPanelURL(want)
	if want == "" {
		return false, fmt.Errorf("empty panel url")
	}
	if _, err := url.Parse(want); err != nil {
		return false, fmt.Errorf("invalid panel url: %w", err)
	}
	// Already using this URL (runtime) — still ensure env file matches.
	envPath := agentEnvFilePath()
	raw, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create minimal env so restart keeps identity.
			content := fmt.Sprintf(
				"AGENT_PANEL_URL=%s\nAGENT_NODE_ID=%s\nAGENT_TOKEN=%s\nAGENT_ROLE=%s\nAGENT_DATA=%s\n",
				want, a.cfg.NodeID, a.cfg.Token, a.cfg.Role, a.cfg.DataDir,
			)
			if err := writeEnvFileAtomic(envPath, content); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, fmt.Errorf("read %s: %w", envPath, err)
	}
	updated, changed, err := rewriteAgentPanelURLEnv(string(raw), want)
	if err != nil {
		return false, err
	}
	cur := normalizeAgentPanelURL(a.cfg.PanelURL)
	if !changed && cur == want {
		return false, nil
	}
	if !changed && cur != want {
		// Runtime differs but file already has target — still need restart to pick env.
		// Fall through to restart path by treating as applied.
		return true, nil
	}
	if err := writeEnvFileAtomic(envPath, updated); err != nil {
		return false, err
	}
	return true, nil
}

func agentEnvFilePath() string {
	if v := strings.TrimSpace(os.Getenv("AGENT_ENV_FILE")); v != "" {
		return v
	}
	return defaultAgentEnvFile
}

func normalizeAgentPanelURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimRight(raw, "/")
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	return raw
}

// rewriteAgentPanelURLEnv updates AGENT_PANEL_URL (and legacy PANEL_URL if present).
func rewriteAgentPanelURLEnv(content, want string) (out string, changed bool, err error) {
	want = normalizeAgentPanelURL(want)
	if want == "" {
		return "", false, fmt.Errorf("empty url")
	}
	lines := strings.Split(content, "\n")
	// Preserve trailing newline semantics: Split keeps last empty if file ends with \n.
	hadAgent := false
	hadLegacy := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		key, val, ok := splitEnvLine(line)
		if !ok {
			continue
		}
		switch key {
		case "AGENT_PANEL_URL":
			hadAgent = true
			cur := normalizeAgentPanelURL(val)
			if cur != want {
				lines[i] = "AGENT_PANEL_URL=" + want
				changed = true
			}
		case "PANEL_URL":
			hadLegacy = true
			cur := normalizeAgentPanelURL(val)
			if cur != want {
				lines[i] = "PANEL_URL=" + want
				changed = true
			}
		}
	}
	if !hadAgent {
		// Insert near top after optional comments, or append.
		insertAt := 0
		for insertAt < len(lines) {
			t := strings.TrimSpace(lines[insertAt])
			if t == "" || strings.HasPrefix(t, "#") {
				insertAt++
				continue
			}
			break
		}
		lines = append(lines[:insertAt], append([]string{"AGENT_PANEL_URL=" + want}, lines[insertAt:]...)...)
		changed = true
	}
	// Keep legacy PANEL_URL in sync only if it already existed.
	_ = hadLegacy
	out = strings.Join(lines, "\n")
	// Ensure trailing newline
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, changed, nil
}

func splitEnvLine(line string) (key, val string, ok bool) {
	// Allow "export KEY=VAL"
	trimLeft := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimLeft, "export ") {
		trimLeft = strings.TrimSpace(strings.TrimPrefix(trimLeft, "export "))
	}
	eq := strings.IndexByte(trimLeft, '=')
	if eq <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(trimLeft[:eq])
	val = strings.TrimSpace(trimLeft[eq+1:])
	// Strip surrounding quotes
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	if key == "" {
		return "", "", false
	}
	return key, val, true
}

func writeEnvFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Cross-device fallback
		if err2 := os.WriteFile(path, []byte(content), 0o600); err2 != nil {
			_ = os.Remove(tmp)
			return err2
		}
		_ = os.Remove(tmp)
	}
	_ = os.Chmod(path, 0o600)
	// Best-effort: if root owns the file, keep mode only.
	return nil
}
