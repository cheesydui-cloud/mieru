package mita

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/plugins/procutil"
)

// Plugin manages mita (mieru server) on Exit nodes.
// Lifecycle mirrors ike-sh/mieru-OneClick + official docs:
//
//	ensure daemon (`mita run` via systemd or managed) → apply config → start → status RUNNING
type Plugin struct {
	DataDir string
	BinDir  string
}

func (p *Plugin) Name() string { return "mita_server" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		return err
	}

	port := toInt(cfg["listen_port"])
	if port <= 0 {
		port = 10001
	}
	pmin := toInt(cfg["port_min"])
	pmax := toInt(cfg["port_max"])

		users := extractUsers(cfg["users"])
		if len(users) == 0 {
			return fmt.Errorf("mita_server: no users — refuse to start (need ≥1 active panel user for auth)")
		}

		// Single primary port by default; multi-port range only when operator set a real span
		// that still includes the primary listen port.
		portBindings := []map[string]interface{}{}
		if pmin > 0 && pmax > pmin {
			// If primary is outside range, still bind primary so probe/relay match.
			if port < pmin || port > pmax {
				portBindings = append(portBindings, map[string]interface{}{
					"port":     port,
					"protocol": "TCP",
				})
			}
			portBindings = append(portBindings, map[string]interface{}{
				"portRange": fmt.Sprintf("%d-%d", pmin, pmax),
				"protocol":  "TCP",
			})
		} else {
			portBindings = append(portBindings, map[string]interface{}{
				"port":     port,
				"protocol": "TCP",
			})
		}

	mitaCfg := map[string]interface{}{
		"portBindings": portBindings,
		"users":        users,
		"loggingLevel": "INFO",
		"mtu":          1400,
	}

	cfgPath := filepath.Join(p.DataDir, "mita-config.json")
	raw, err := json.MarshalIndent(mitaCfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(p.DataDir, "mita-users.json"), raw, 0o600)
	log.Printf("[mita_server] wrote %s users=%d port=%d", cfgPath, len(users), port)

	binDir := p.BinDir
	if binDir == "" {
		binDir = filepath.Join(p.DataDir, "bin")
	}
	bin, err := procutil.EnsureBinary("mita", binDir)
	if err != nil {
		return fmt.Errorf("mita binary: %w", err)
	}

	rt, err := procutil.EnsureMitaDaemon(bin, p.DataDir)
	if err != nil {
		return fmt.Errorf("mita daemon: %w", err)
	}

	// apply config with retries (OneClick apply_config)
	var applyOut string
	var applyErr error
	for i := 0; i < 5; i++ {
		applyOut, applyErr = rt.MitaCmd("apply", "config", cfgPath)
		if applyErr == nil {
			log.Printf("[mita_server] apply ok: %s", strings.TrimSpace(applyOut))
			break
		}
		log.Printf("[mita_server] apply attempt %d: %v (%s)", i+1, applyErr, strings.TrimSpace(applyOut))
		// re-ensure daemon
		if rt2, e2 := procutil.EnsureMitaDaemon(bin, p.DataDir); e2 == nil {
			rt = rt2
		}
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if applyErr != nil {
		return fmt.Errorf("mita apply config: %w (%s)", applyErr, strings.TrimSpace(applyOut))
	}

	// stop then start so portBindings take effect (official: apply needs restart except users-only)
	_, _ = rt.MitaCmd("stop")
	time.Sleep(400 * time.Millisecond)

	var startOut string
	var startErr error
	for i := 0; i < 5; i++ {
		startOut, startErr = rt.MitaCmd("start")
		if startErr == nil {
			break
		}
		log.Printf("[mita_server] start attempt %d: %v (%s)", i+1, startErr, strings.TrimSpace(startOut))
		if rt2, e2 := procutil.EnsureMitaDaemon(bin, p.DataDir); e2 == nil {
			rt = rt2
		}
		time.Sleep(time.Duration(i+1) * time.Second)
	}
	if startErr != nil {
		return fmt.Errorf("mita start: %w (%s)", startErr, strings.TrimSpace(startOut))
	}
	log.Printf("[mita_server] start ok: %s", strings.TrimSpace(startOut))

	// verify RUNNING
	for i := 0; i < 8; i++ {
		time.Sleep(500 * time.Millisecond)
		status, _ := rt.MitaCmd("status")
		status = strings.TrimSpace(status)
		log.Printf("[mita_server] status: %s", status)
		if strings.Contains(strings.ToUpper(status), "RUNNING") {
			return nil
		}
		_, _ = rt.MitaCmd("start")
	}
	status, _ := rt.MitaCmd("status")
	return fmt.Errorf("mita not RUNNING after start (status=%s)", strings.TrimSpace(status))
}

// extractUsers accepts []interface{}, []map, or JSON-like maps from agent injection.
func extractUsers(v interface{}) []map[string]string {
	out := []map[string]string{}
	appendOne := func(m map[string]interface{}) {
		name, _ := m["username"].(string)
		if name == "" {
			name, _ = m["name"].(string)
		}
		if name == "" {
			name, _ = m["mita_user"].(string)
		}
		pass, _ := m["password"].(string)
		enabled := true
		if e, ok := m["enabled"].(bool); ok {
			enabled = e
		}
		// json numbers / missing enabled default true
		if name != "" && pass != "" && enabled {
			out = append(out, map[string]string{"name": name, "password": pass})
		}
	}

	switch t := v.(type) {
	case []interface{}:
		for _, it := range t {
			if m, ok := it.(map[string]interface{}); ok {
				appendOne(m)
			}
		}
	case []map[string]interface{}:
		for _, m := range t {
			appendOne(m)
		}
	case []map[string]string:
		for _, m := range t {
			name := m["username"]
			if name == "" {
				name = m["name"]
			}
			pass := m["password"]
			if name != "" && pass != "" {
				out = append(out, map[string]string{"name": name, "password": pass})
			}
		}
	default:
		// try re-marshal (handles []model.AgentUser if ever passed raw)
		if v == nil {
			return out
		}
		b, err := json.Marshal(v)
		if err != nil {
			return out
		}
		var arr []map[string]interface{}
		if json.Unmarshal(b, &arr) == nil {
			for _, m := range arr {
				appendOne(m)
			}
		}
	}
	return out
}

func toInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}
