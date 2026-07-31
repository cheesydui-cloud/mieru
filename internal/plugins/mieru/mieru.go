package mieru

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

// Plugin manages mieru client on Relay (connects to Exit mita).
// Official flow: apply config → stop → start (client has no separate `run` daemon requirement;
// `mieru start` backgrounds the client).
type Plugin struct {
	DataDir string
	BinDir  string
}

func (p *Plugin) Name() string { return "mieru_client" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		return err
	}

	server, _ := cfg["server"].(string)
	port := toInt(cfg["port"])
	if server == "" || port <= 0 {
		return fmt.Errorf("mieru_client: server and port required")
	}

		socksPort := toInt(cfg["socks5_port"])
		if socksPort <= 0 {
			socksPort = 19080
		}
		rpcPort := toInt(cfg["rpc_port"])
		if rpcPort <= 0 {
			rpcPort = 18964 // private control port; never default to public 8964
		}
		if rpcPort == socksPort {
			rpcPort = socksPort + 1
			if rpcPort > 65535 {
				rpcPort = socksPort - 1
			}
		}

	linkUser, _ := cfg["link_user"].(string)
	linkPass, _ := cfg["link_password"].(string)
	if linkUser == "" || linkPass == "" {
		for _, u := range extractUsers(cfg["users"]) {
			linkUser, linkPass = u["name"], u["password"]
			break
		}
	}
	if linkUser == "" || linkPass == "" {
		return fmt.Errorf("mieru_client: no tunnel user/password (need active panel user or link_user)")
	}

	mieruCfg := map[string]interface{}{
		"profiles": []map[string]interface{}{
			{
				"profileName": "panel-exit",
				"user": map[string]string{
					"name":     linkUser,
					"password": linkPass,
				},
				"servers": []map[string]interface{}{
					{
						"ipAddress":  server,
						"domainName": "",
						"portBindings": []map[string]interface{}{
							{"port": port, "protocol": "TCP"},
						},
					},
				},
				"mtu": 1400,
				"multiplexing": map[string]string{
					"level": "MULTIPLEXING_HIGH",
				},
				"handshakeMode": "HANDSHAKE_STANDARD",
			},
		},
		"activeProfile":      "panel-exit",
		"rpcPort":            rpcPort,
		"socks5Port":         socksPort,
		"loggingLevel":       "INFO",
		"socks5ListenLAN":    true,
		"httpProxyListenLAN": false,
	}

	cfgPath := filepath.Join(p.DataDir, "mieru-config.json")
	raw, err := json.MarshalIndent(mieruCfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(p.DataDir, "mieru-client.json"), raw, 0o600)
	_ = os.WriteFile(filepath.Join(p.DataDir, "socks5.port"), []byte(strconv.Itoa(socksPort)), 0o644)
	log.Printf("[mieru_client] wrote %s → %s:%d socks5=:%d user=%s", cfgPath, server, port, socksPort, linkUser)

	binDir := p.BinDir
	if binDir == "" {
		binDir = filepath.Join(p.DataDir, "bin")
	}
	bin, err := procutil.EnsureBinary("mieru", binDir)
	if err != nil {
		return fmt.Errorf("mieru binary: %w", err)
	}

	// Prefer explicit config path via env so apply is reliable for non-interactive agent user.
	env := []string{
		"MIERU_CONFIG_JSON_FILE=" + cfgPath,
	}

	// apply (merge into client config store)
	var applyOut string
	var applyErr error
	for i := 0; i < 3; i++ {
		applyOut, applyErr = procutil.RunCaptureEnv(env, bin, "apply", "config", cfgPath)
		if applyErr == nil {
			log.Printf("[mieru_client] apply ok: %s", strings.TrimSpace(applyOut))
			break
		}
		// fallback without env
		applyOut, applyErr = procutil.RunCapture(bin, "apply", "config", cfgPath)
		if applyErr == nil {
			log.Printf("[mieru_client] apply ok (no env): %s", strings.TrimSpace(applyOut))
			break
		}
		log.Printf("[mieru_client] apply attempt %d: %v (%s)", i+1, applyErr, strings.TrimSpace(applyOut))
		time.Sleep(time.Second)
	}
	if applyErr != nil {
		return fmt.Errorf("mieru apply: %w (%s)", applyErr, strings.TrimSpace(applyOut))
	}

	_, _ = procutil.RunCaptureEnv(env, bin, "stop")
	_, _ = procutil.RunCapture(bin, "stop")
	time.Sleep(400 * time.Millisecond)

	var startOut string
	var startErr error
	for i := 0; i < 5; i++ {
		startOut, startErr = procutil.RunCaptureEnv(env, bin, "start")
		if startErr != nil {
			startOut, startErr = procutil.RunCapture(bin, "start")
		}
		if startErr == nil {
			break
		}
		log.Printf("[mieru_client] start attempt %d: %v (%s)", i+1, startErr, strings.TrimSpace(startOut))
		time.Sleep(time.Duration(i+1) * 500 * time.Millisecond)
	}
	if startErr != nil {
		return fmt.Errorf("mieru start: %w (%s)", startErr, strings.TrimSpace(startOut))
	}
	log.Printf("[mieru_client] started socks5 127.0.0.1:%d → %s:%d (%s)", socksPort, server, port, strings.TrimSpace(startOut))

	// non-fatal connectivity test (exit may not be up yet)
	if out, err := procutil.RunCaptureEnv(env, bin, "test"); err != nil {
		if out2, err2 := procutil.RunCapture(bin, "test"); err2 != nil {
			log.Printf("[mieru_client] test (non-fatal): %v (%s)", err2, strings.TrimSpace(out2))
		} else {
			log.Printf("[mieru_client] test ok: %s", strings.TrimSpace(out2))
		}
		_ = out
	} else {
		log.Printf("[mieru_client] test ok: %s", strings.TrimSpace(out))
	}
	return nil
}

func extractUsers(v interface{}) []map[string]string {
	out := []map[string]string{}
	appendOne := func(m map[string]interface{}) {
		name, _ := m["username"].(string)
		if name == "" {
			name, _ = m["name"].(string)
		}
		pass, _ := m["password"].(string)
		enabled := true
		if e, ok := m["enabled"].(bool); ok {
			enabled = e
		}
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
