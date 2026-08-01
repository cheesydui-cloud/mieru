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
//	ensure daemon (`mita run` via systemd or managed) → apply config → stop → start → RUNNING
//
// Users include allowPrivateIP/allowLoopbackIP so relay→exit over IX LAN works.
type Plugin struct {
	DataDir string
	BinDir  string
}

func (p *Plugin) Name() string { return "mita_server" }

// Stop implements plugins.Stopper — halt mita when node is deleted / revoked.
func (p *Plugin) Stop() {
	binDir := p.BinDir
	if binDir == "" {
		binDir = filepath.Join(p.DataDir, "bin")
	}
	bin, err := procutil.EnsureBinary("mita", binDir)
	if err != nil {
		log.Printf("[mita_server] stop: no binary: %v", err)
		return
	}
	rt, err := procutil.EnsureMitaDaemon(bin, p.DataDir)
	if err != nil {
		// Still try bare stop without managed runtime
		if out, e := procutil.RunCapture(bin, "stop"); e != nil {
			log.Printf("[mita_server] stop: %v (%s)", e, strings.TrimSpace(out))
		} else {
			log.Printf("[mita_server] stopped (bare)")
		}
		return
	}
	if out, e := rt.MitaCmd("stop"); e != nil {
		log.Printf("[mita_server] stop: %v (%s)", e, strings.TrimSpace(out))
	} else {
		log.Printf("[mita_server] stopped")
	}
}

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

	// Single primary port by default; multi-port range only when operator set a real span.
	// OneClick binds TCP (and optionally UDP); panel data-plane is TCP end-to-end.
	portBindings := []map[string]interface{}{}
	addBinding := func(proto string) {
		if pmin > 0 && pmax > pmin {
			if port < pmin || port > pmax {
				portBindings = append(portBindings, map[string]interface{}{
					"port":     port,
					"protocol": proto,
				})
			}
			portBindings = append(portBindings, map[string]interface{}{
				"portRange": fmt.Sprintf("%d-%d", pmin, pmax),
				"protocol":  proto,
			})
		} else {
			portBindings = append(portBindings, map[string]interface{}{
				"port":     port,
				"protocol": proto,
			})
		}
	}
	addBinding("TCP")
	// Optional UDP if operator asks (default off — matches simple OneClick TCP install).
	if v, ok := cfg["enable_udp"].(bool); ok && v {
		addBinding("UDP")
	}

	mtu := toInt(cfg["mtu"])
	if mtu < 1280 || mtu > 1500 {
		mtu = 1400
	}

	// Server shape mirrors OneClick write_server_config (single-instance path).
	mitaCfg := map[string]interface{}{
		"portBindings": portBindings,
		"users":        users,
		"loggingLevel": "INFO",
		"mtu":          mtu,
	}

	cfgPath := filepath.Join(p.DataDir, "mita-config.json")
	raw, err := json.MarshalIndent(mitaCfg, "", "  ")
	if err != nil {
		return err
	}
	desiredNames := make([]string, 0, len(users))
	for _, u := range users {
		if n, _ := u["name"].(string); n != "" {
			desiredNames = append(desiredNames, n)
		}
	}

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

	// If desired config bytes match last apply and mita is already RUNNING,
	// do not stop/start — that drops every live client session.
	if prev, err := os.ReadFile(cfgPath); err == nil && string(prev) == string(raw) {
		status, _ := rt.MitaCmd("status")
		if strings.Contains(strings.ToUpper(strings.TrimSpace(status)), "RUNNING") {
			return nil
		}
		// Config same but not running — just start.
		if _, startErr := rt.MitaCmd("start"); startErr == nil {
			for i := 0; i < 10; i++ {
				time.Sleep(500 * time.Millisecond)
				status, _ = rt.MitaCmd("status")
				if strings.Contains(strings.ToUpper(strings.TrimSpace(status)), "RUNNING") {
					return nil
				}
				_, _ = rt.MitaCmd("start")
			}
		}
		// fall through to full apply path
	}

	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(p.DataDir, "mita-users.json"), raw, 0o600)
	log.Printf("[mita_server] wrote %s users=%d port=%d names=%v", cfgPath, len(users), port, desiredNames)

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

	// OneClick: mita apply MERGES users — delete stale names so revoked passwords disappear.
	if err := syncMitaUsers(rt, desiredNames); err != nil {
		log.Printf("[mita_server] user sync warning: %v", err)
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

	// verify RUNNING (OneClick: status is "RUNNING")
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		status, _ := rt.MitaCmd("status")
		status = strings.TrimSpace(status)
		log.Printf("[mita_server] status: %s", status)
		up := strings.ToUpper(status)
		if strings.Contains(up, "RUNNING") {
			return nil
		}
		_, _ = rt.MitaCmd("start")
	}
	status, _ := rt.MitaCmd("status")
	return fmt.Errorf("mita not RUNNING after start (status=%s)", strings.TrimSpace(status))
}

// syncMitaUsers deletes mita users that are not in the desired set.
// mita apply merges users; without this, old passwords keep working.
func syncMitaUsers(rt *procutil.MitaRuntime, desired []string) error {
	if rt == nil || len(desired) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, n := range desired {
		want[n] = true
	}
	// Prefer structured describe; fall back to get users text.
	desc, err := rt.MitaCmd("describe", "config")
	actual := parseUserNamesFromDescribe(desc)
	if len(actual) == 0 {
		if out, e2 := rt.MitaCmd("get", "users"); e2 == nil {
			actual = parseUserNamesFromGetUsers(out)
		}
		_ = err
	}
	for _, name := range actual {
		if want[name] {
			continue
		}
		log.Printf("[mita_server] delete stale user %q", name)
		if out, e := rt.MitaCmd("delete", "user", name); e != nil {
			log.Printf("[mita_server] delete user %s: %v (%s)", name, e, strings.TrimSpace(out))
		}
	}
	return nil
}

func parseUserNamesFromDescribe(desc string) []string {
	var cfg struct {
		Users []struct {
			Name string `json:"name"`
		} `json:"users"`
	}
	if json.Unmarshal([]byte(desc), &cfg) != nil {
		// describe may wrap or pretty-print with noise — try find JSON object
		i := strings.Index(desc, "{")
		j := strings.LastIndex(desc, "}")
		if i >= 0 && j > i {
			_ = json.Unmarshal([]byte(desc[i:j+1]), &cfg)
		}
	}
	out := make([]string, 0, len(cfg.Users))
	for _, u := range cfg.Users {
		if u.Name != "" {
			out = append(out, u.Name)
		}
	}
	return out
}

func parseUserNamesFromGetUsers(out string) []string {
	// best-effort: lines / json array
	var arr []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(out), &arr) == nil {
		names := make([]string, 0, len(arr))
		for _, u := range arr {
			if u.Name != "" {
				names = append(names, u.Name)
			}
		}
		return names
	}
	// fallback: look for "name": "..."
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") || strings.Contains(line, `"name"`) {
			// crude extract
			if i := strings.Index(line, ":"); i >= 0 {
				n := strings.Trim(strings.TrimSpace(line[i+1:]), `",`)
				if n != "" && n != "name" {
					names = append(names, n)
				}
			}
		}
	}
	return names
}

// extractUsers builds mita user objects.
// allowPrivateIP / allowLoopbackIP are required for panel multi-hop:
// relay often dials exit via IX private IP or 127.0.0.1 on hybrid.
func extractUsers(v interface{}) []map[string]interface{} {
	out := []map[string]interface{}{}
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
		if name != "" && pass != "" && enabled {
			out = append(out, map[string]interface{}{
				"name":            name,
				"password":        pass,
				"allowPrivateIP":  true,
				"allowLoopbackIP": true,
			})
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
				out = append(out, map[string]interface{}{
					"name":            name,
					"password":        pass,
					"allowPrivateIP":  true,
					"allowLoopbackIP": true,
				})
			}
		}
	default:
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
