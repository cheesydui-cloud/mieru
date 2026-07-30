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
		rpcPort = 8964
	}

	linkUser, _ := cfg["link_user"].(string)
	linkPass, _ := cfg["link_password"].(string)
	if linkUser == "" || linkPass == "" {
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
				pass, _ := m["password"].(string)
				enabled := true
				if e, ok := m["enabled"].(bool); ok {
					enabled = e
				}
				if name != "" && pass != "" && enabled {
					linkUser, linkPass = name, pass
					break
				}
			}
		}
	}
	if linkUser == "" || linkPass == "" {
		return fmt.Errorf("mieru_client: no tunnel user/password (need users or link_user)")
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

	if out, err := procutil.RunCapture(bin, "apply", "config", cfgPath); err != nil {
		log.Printf("[mieru_client] apply: %v (%s)", err, strings.TrimSpace(out))
	} else {
		log.Printf("[mieru_client] apply ok: %s", strings.TrimSpace(out))
	}

	_, _ = procutil.RunCapture(bin, "stop")
	time.Sleep(300 * time.Millisecond)
	if out, err := procutil.RunCapture(bin, "start"); err != nil {
		return fmt.Errorf("mieru start: %w (%s)", err, strings.TrimSpace(out))
	}
	log.Printf("[mieru_client] started socks5 127.0.0.1:%d → %s:%d", socksPort, server, port)

	if out, err := procutil.RunCapture(bin, "test"); err != nil {
		log.Printf("[mieru_client] test (non-fatal): %v (%s)", err, strings.TrimSpace(out))
	} else {
		log.Printf("[mieru_client] test ok: %s", strings.TrimSpace(out))
	}
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
