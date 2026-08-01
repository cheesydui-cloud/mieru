package mieru

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/plugins/procutil"
)

// Plugin manages mieru client on Relay (connects to Exit mita).
// Client JSON aligned with ike-sh/mieru-OneClick build_client_json_for:
//
//	multiplexing OFF, handshake HANDSHAKE_NO_WAIT, mtu 1400
//
// Official flow (v0.3.3): write patch → wipe bad store → apply into store → stop → start.
// Do NOT set httpProxyPort=0 (ValidateFullClientConfig rejects it).
// Do NOT skip apply: apply hashes the password and merges into the live store.
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
	server = strings.TrimSpace(server)

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

	// Optional overrides (defaults match OneClick 2.1.1 recommended values).
	muxLevel, _ := cfg["multiplexing"].(string)
	if muxLevel == "" {
		muxLevel = "MULTIPLEXING_OFF"
	}
	handshake, _ := cfg["handshake_mode"].(string)
	if handshake == "" {
		handshake = "HANDSHAKE_NO_WAIT"
	}
	mtu := toInt(cfg["mtu"])
	if mtu < 1280 || mtu > 1500 {
		mtu = 1400
	}

	// ipAddress vs domainName — official/OneClick set one; omit the other.
	serverEntry := map[string]interface{}{
		"portBindings": []map[string]interface{}{},
	}
	if ip := net.ParseIP(server); ip != nil {
		serverEntry["ipAddress"] = server
		serverEntry["domainName"] = ""
	} else {
		serverEntry["domainName"] = server
		serverEntry["ipAddress"] = ""
	}

	protocol, _ := cfg["protocol"].(string)
	if protocol == "" {
		protocol = "TCP"
	}
	protocol = strings.ToUpper(protocol)
	serverEntry["portBindings"] = []map[string]interface{}{
		{"port": port, "protocol": protocol},
	}

	// Patch for `mieru apply config`. Omit httpProxyPort entirely — value 0 is invalid.
	mieruCfg := map[string]interface{}{
		"profiles": []map[string]interface{}{
			{
				"profileName": "default",
				"user": map[string]string{
					"name":     linkUser,
					"password": linkPass,
				},
				"servers": []map[string]interface{}{serverEntry},
				"mtu":     mtu,
				"multiplexing": map[string]string{
					"level": muxLevel,
				},
				"handshakeMode": handshake,
			},
		},
		"activeProfile":   "default",
		"rpcPort":         rpcPort,
		"socks5Port":      socksPort,
		"loggingLevel":    "INFO",
		"socks5ListenLAN": true, // local socks_in on 127.0.0.1 must reach it
	}

		// patch file = what we apply; store file = live config (MIERU_CONFIG_JSON_FILE).
		// Keeping them separate matches official client usage and lets apply hash passwords.
		patchPath := filepath.Join(p.DataDir, "mieru-patch.json")
		storePath := filepath.Join(p.DataDir, "mieru-config.json")
		raw, err := json.MarshalIndent(mieruCfg, "", "  ")
		if err != nil {
			return err
		}

		// Idempotent: same patch + local socks still accepting → do not stop/start
		// (periodic agent re-apply would drop hybrid/relay client sessions).
		if prev, err := os.ReadFile(patchPath); err == nil && string(prev) == string(raw) {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
			if c, err := net.DialTimeout("tcp", addr, 400*time.Millisecond); err == nil {
				_ = c.Close()
				return nil
			}
		}

		if err := os.WriteFile(patchPath, raw, 0o600); err != nil {
			return err
		}
		// Keep debug copy under the old name for ops.
		_ = os.WriteFile(filepath.Join(p.DataDir, "mieru-client.json"), raw, 0o600)
		_ = os.WriteFile(filepath.Join(p.DataDir, "socks5.port"), []byte(strconv.Itoa(socksPort)), 0o644)

		// Drop legacy / poisoned stores. apply merges into existing store and will fail
		// if the store still has httpProxyPort:0 from older agents.
		for _, stale := range []string{
			storePath,
			filepath.Join(p.DataDir, "client-store.json"),
			filepath.Join(p.DataDir, "mieru.json"),
		} {
			_ = os.Remove(stale)
		}
		// Also scrub any other json under data dir that looks like a mieru store with port 0.
		scrubHTTPProxyPortZero(p.DataDir)

		log.Printf("[mieru_client] wrote patch %s → %s:%d socks5=:%d user=%s mux=%s hs=%s",
			patchPath, server, port, socksPort, linkUser, muxLevel, handshake)

	binDir := p.BinDir
	if binDir == "" {
		binDir = filepath.Join(p.DataDir, "bin")
	}
	bin, err := procutil.EnsureBinary("mieru", binDir)
	if err != nil {
		return fmt.Errorf("mieru binary: %w", err)
	}

	// Live store path — apply merges patch into this file; start/stop/test read it.
	env := []string{
		"MIERU_CONFIG_JSON_FILE=" + storePath,
	}

	// apply (merge into client config store) — required so password is hashed correctly
	var applyOut string
	var applyErr error
	for i := 0; i < 3; i++ {
		// Fresh empty store each attempt so a half-written poison cannot stick.
		_ = os.Remove(storePath)
		applyOut, applyErr = procutil.RunCaptureEnv(env, bin, "apply", "config", patchPath)
		if applyErr == nil {
			log.Printf("[mieru_client] apply ok: %s", strings.TrimSpace(applyOut))
			break
		}
		// fallback without env (default ~/.config/mieru/client_config.json path)
		applyOut, applyErr = procutil.RunCapture(bin, "apply", "config", patchPath)
		if applyErr == nil {
			log.Printf("[mieru_client] apply ok (no env): %s", strings.TrimSpace(applyOut))
			break
		}
		log.Printf("[mieru_client] apply attempt %d: %v (%s)", i+1, applyErr, strings.TrimSpace(applyOut))
		// If still poisoned, force-wipe again
		_ = os.Remove(storePath)
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

	// Connectivity test — soft-fail; hard-fail if socks not listening.
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

	// Verify local socks5 is actually accepting (hard requirement for relay chain).
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(socksPort))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("mieru started but local socks5 %s not accepting connections", addr)
}

// scrubHTTPProxyPortZero removes or rewrites json files under dir that contain
// "httpProxyPort": 0, which breaks subsequent `mieru apply` merges.
func scrubHTTPProxyPortZero(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// cheap check first
		if !strings.Contains(string(b), "httpProxyPort") {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		if v, ok := m["httpProxyPort"]; ok {
			// drop 0 / 0.0 / "0"
			switch t := v.(type) {
			case float64:
				if t == 0 {
					delete(m, "httpProxyPort")
				}
			case int:
				if t == 0 {
					delete(m, "httpProxyPort")
				}
			case string:
				if t == "0" || t == "" {
					delete(m, "httpProxyPort")
				}
			case nil:
				delete(m, "httpProxyPort")
			}
			if _, still := m["httpProxyPort"]; !still {
				// if this looks like a full store and we only fixed port 0, rewrite;
				// safer for unknown files: just delete so apply starts clean.
				_ = os.Remove(path)
				log.Printf("[mieru_client] removed poisoned config with httpProxyPort=0: %s", path)
			}
		}
	}
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
