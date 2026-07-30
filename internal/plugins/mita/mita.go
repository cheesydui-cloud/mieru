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

	"github.com/cheesydui-cloud/mieru/internal/plugins/procutil"
)

// Plugin manages mita (mieru server) on Exit nodes.
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

	users := []map[string]string{}
	if u, ok := cfg["users"].([]interface{}); ok {
		for _, it := range u {
			m, ok := it.(map[string]interface{})
			if !ok {
				continue
			}
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
			if name != "" && pass != "" && enabled {
				users = append(users, map[string]string{"name": name, "password": pass})
			}
		}
	}
	if len(users) == 0 {
		log.Printf("[mita_server] warning: no users in config")
	}

	portBindings := []map[string]interface{}{}
	if pmin > 0 && pmax >= pmin && pmax != pmin {
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

	if out, err := procutil.RunCapture(bin, "apply", "config", cfgPath); err != nil {
		log.Printf("[mita_server] apply config: %v (%s)", err, strings.TrimSpace(out))
	} else {
		log.Printf("[mita_server] apply ok: %s", strings.TrimSpace(out))
	}

	status, _ := procutil.RunCapture(bin, "status")
	status = strings.TrimSpace(status)
	log.Printf("[mita_server] status: %s", status)

	if strings.Contains(strings.ToUpper(status), "RUNNING") {
		if out, err := procutil.RunCapture(bin, "reload"); err != nil {
			log.Printf("[mita_server] reload: %v (%s) — restarting", err, strings.TrimSpace(out))
			_, _ = procutil.RunCapture(bin, "stop")
			if out2, err2 := procutil.RunCapture(bin, "start"); err2 != nil {
				return fmt.Errorf("mita start: %w (%s)", err2, strings.TrimSpace(out2))
			}
		} else {
			log.Printf("[mita_server] reloaded")
		}
	} else {
		if out, err := procutil.RunCapture(bin, "start"); err != nil {
			return fmt.Errorf("mita start: %w (%s)", err, strings.TrimSpace(out))
		}
		log.Printf("[mita_server] started")
	}

	status, _ = procutil.RunCapture(bin, "status")
	log.Printf("[mita_server] final status: %s", strings.TrimSpace(status))
	return nil
}

func toInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}
