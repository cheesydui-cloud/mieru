package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/plugins"
	"github.com/cheesydui-cloud/mieru/internal/plugins/mieru"
	"github.com/cheesydui-cloud/mieru/internal/plugins/mita"
	"github.com/cheesydui-cloud/mieru/internal/plugins/nftables"
	"github.com/cheesydui-cloud/mieru/internal/plugins/socksin"
	"github.com/cheesydui-cloud/mieru/internal/plugins/tcpforward"
)

// AgentVersion is the default when main does not inject a build version.
// Prefer SetVersion() from cmd/agent so -version and heartbeat always match.
var AgentVersion = "0.3.12"

// SetVersion overrides the version string reported in heartbeats (and logs).
// Call from main with the same value as -ldflags -X main.Version.
func SetVersion(v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	// Heartbeat / UI historically use bare semver; strip optional leading "v".
	AgentVersion = strings.TrimPrefix(v, "v")
}

type Agent struct {
	cfg      config.AgentConfig
	client   *http.Client
	registry *plugins.Registry
	// simple local counters for demo metering on exit
	counters map[int64]*userCounter
	// stateMu guards version + lastApplyMsg (heartbeat vs background apply).
	stateMu      sync.Mutex
	version      int64
	lastApplyMsg string
	// applyMu serializes pullAndApply so heartbeat never blocks on plugin apply.
	applyMu   chan struct{}
	applyBusy int32 // atomic: 1 while apply running
}

type userCounter struct {
	up, down int64
}

func New(cfg config.AgentConfig) *Agent {
	reg := plugins.NewRegistry()
	data := cfg.DataDir
	binDir := filepath.Join(data, "bin")
		reg.Register(&nftables.Plugin{DataDir: filepath.Join(data, "nft"), DryRun: os.Getenv("AGENT_NFT_DRYRUN") != "0"})
		reg.Register(&mieru.Plugin{DataDir: filepath.Join(data, "mieru"), BinDir: binDir})
		reg.Register(&mita.Plugin{DataDir: filepath.Join(data, "mita"), BinDir: binDir})
		reg.Register(&socksin.Plugin{DataDir: filepath.Join(data, "socks")})
		reg.Register(&tcpforward.Plugin{DataDir: filepath.Join(data, "tcpforward")})

	return &Agent{
		cfg:      cfg,
		client:   &http.Client{Timeout: 15 * time.Second},
		registry: reg,
		counters: map[int64]*userCounter{},
		applyMu:  make(chan struct{}, 1),
	}
}

func (a *Agent) Run(ctx context.Context) error {
	if a.cfg.NodeID == "" || a.cfg.Token == "" {
		return fmt.Errorf("AGENT_NODE_ID and AGENT_TOKEN are required")
	}
	if err := os.MkdirAll(a.cfg.DataDir, 0o755); err != nil {
		return err
	}

	// load last_good version
	if b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "version")); err == nil {
		var v int64
		_, _ = fmt.Sscanf(string(b), "%d", &v)
		a.version = v
	}

		panel := strings.TrimRight(strings.TrimSpace(a.cfg.PanelURL), "/")
		if panel == "" {
			return fmt.Errorf("AGENT_PANEL_URL is required")
		}
		a.cfg.PanelURL = panel
		log.Printf("agent start version=%s panel=%s node=%s role=%s", AgentVersion, panel, a.cfg.NodeID, a.cfg.Role)
	log.Printf("agent starting node=%s role=%s panel=%s", a.cfg.NodeID, a.cfg.Role, a.cfg.PanelURL)

	// Immediate heartbeat so panel shows online without waiting for first ticker.
	if err := a.heartbeat(ctx); err != nil {
		log.Printf("initial heartbeat FAILED: %v", err)
		log.Printf("check: 1) panel reachable from this host  2) node_id/token match  3) firewall allows panel :8080")
	} else {
		log.Printf("initial heartbeat OK — panel should show this node online")
	}

	// initial pull in background so first heartbeats (and dial jobs) are not blocked
	a.schedulePull(ctx)

	// Faster heartbeat improves hop-probe reliability (panel waits ~22s for dial job).
	hbEvery := a.cfg.HeartbeatEvery
	if hbEvery > 5*time.Second {
		hbEvery = 5 * time.Second
	}
	hb := time.NewTicker(hbEvery)
	pull := time.NewTicker(a.cfg.PullEvery)
	traffic := time.NewTicker(2 * time.Second)
	defer hb.Stop()
	defer pull.Stop()
	defer traffic.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-hb.C:
			if err := a.heartbeat(ctx); err != nil {
				log.Printf("heartbeat: %v", err)
			}
		case <-pull.C:
			a.schedulePull(ctx)
		case <-traffic.C:
			if a.cfg.Role == model.RoleExit || a.cfg.Role == model.RoleHybrid {
				if err := a.reportTraffic(ctx); err != nil {
					log.Printf("traffic: %v", err)
				}
			}
		}
	}
}

// schedulePull runs pullAndApply in the background so heartbeats keep flowing
// (hop-to-hop probe jobs are delivered on heartbeat).
func (a *Agent) schedulePull(ctx context.Context) {
	select {
	case a.applyMu <- struct{}{}:
	default:
		// already applying or queued
		return
	}
	go func() {
		defer func() { <-a.applyMu }()
		atomic.StoreInt32(&a.applyBusy, 1)
		defer atomic.StoreInt32(&a.applyBusy, 0)
		if err := a.pullAndApply(ctx); err != nil {
			log.Printf("pull: %v", err)
		}
	}()
}

func (a *Agent) heartbeat(ctx context.Context) error {
	msg := ""
	applyErr := ""
	a.stateMu.Lock()
	ver := a.version
	if a.lastApplyMsg != "" {
		msg = "degraded"
		// Truncate so heartbeat payload stays small.
		applyErr = a.lastApplyMsg
		if len(applyErr) > 800 {
			applyErr = applyErr[:800] + "…"
		}
	}
	a.stateMu.Unlock()
		body := model.HeartbeatRequest{
			NodeID:        a.cfg.NodeID,
			Token:         a.cfg.Token,
			Role:          a.cfg.Role,
			ConfigVersion: ver,
			AgentVersion:  AgentVersion, // same source as CLI after SetVersion
			Hostname:      os.Getenv("AGENT_HOSTNAME"),
			PublicIP:      os.Getenv("AGENT_PUBLIC_IP"),
			Message:       msg,
			ApplyError:    applyErr,
		}
		var resp struct {
		OK            bool  `json:"ok"`
		ConfigVersion int64 `json:"config_version"`
		NeedPull      bool  `json:"need_pull"`
		DialJobs      []struct {
			ID        string `json:"id"`
			Host      string `json:"host"`
			Port      int    `json:"port"`
			TimeoutMS int    `json:"timeout_ms"`
		} `json:"dial_jobs"`
	}
	if err := a.postJSON(ctx, "/api/agent/heartbeat", body, &resp); err != nil {
		return err
	}
	// Hop-to-hop probe jobs FIRST — never wait on apply for these.
	for _, job := range resp.DialJobs {
		if job.ID == "" || job.Host == "" || job.Port <= 0 {
			continue
		}
		timeout := time.Duration(job.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 4 * time.Second
		}
		ok, lat, errMsg := tcpDial(job.Host, job.Port, timeout)
		if err := a.postJSON(ctx, "/api/agent/dial-result", map[string]interface{}{
			"node_id":    a.cfg.NodeID,
			"token":      a.cfg.Token,
			"job_id":     job.ID,
			"ok":         ok,
			"latency_ms": lat,
			"error":      errMsg,
		}, nil); err != nil {
			log.Printf("dial-result job=%s: %v", job.ID, err)
		} else {
			log.Printf("dial-result job=%s %s:%d ok=%v lat=%dms %s",
				job.ID, job.Host, job.Port, ok, lat, errMsg)
		}
	}
	if resp.NeedPull {
		a.schedulePull(ctx)
	}
	return nil
}

// tcpDial measures TCP connect latency from this host to host:port.
func tcpDial(host string, port int, timeout time.Duration) (ok bool, latencyMs int64, errMsg string) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, timeout)
	latencyMs = time.Since(start).Milliseconds()
	if err != nil {
		return false, latencyMs, err.Error()
	}
	_ = conn.Close()
	return true, latencyMs, ""
}

func (a *Agent) pullAndApply(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.PanelURL+"/api/agent/config", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Node-ID", a.cfg.NodeID)
	req.Header.Set("X-Node-Token", a.cfg.Token)
	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != 200 {
		return fmt.Errorf("config status %d: %s", res.StatusCode, string(raw))
	}
	var cfg model.AgentDesiredConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}

	// persist last_good payload (even on failed apply — useful for debug)
	_ = os.WriteFile(filepath.Join(a.cfg.DataDir, "desired.json"), raw, 0o600)

	if err := a.apply(ctx, &cfg); err != nil {
		a.stateMu.Lock()
		a.lastApplyMsg = err.Error()
		a.stateMu.Unlock()
		// Do NOT advance version on failure/partial required-plugin failure —
		// next pull/heartbeat will retry the same config version.
		return err
	}
	a.stateMu.Lock()
	a.lastApplyMsg = ""
	a.version = cfg.Version
	a.stateMu.Unlock()
	_ = os.WriteFile(filepath.Join(a.cfg.DataDir, "version"), []byte(fmt.Sprintf("%d", cfg.Version)), 0o644)
	log.Printf("applied config version=%d plugins=%d users=%d", cfg.Version, len(cfg.Plugins), len(cfg.Users))
	return nil
}

func (a *Agent) apply(ctx context.Context, cfg *model.AgentDesiredConfig) error {
		// Apply order: mita/mieru first, then public listeners (socks / tcp_forward), then nft.
		order := map[string]int{
			"mita_server":  10,
			"mieru_client": 20,
			"socks_in":     30,
			"tcp_forward":  30,
			"nft_forward":  40,
		}
	// Role-required plugins must succeed before we accept the config version.
	required := requiredPlugins(cfg)
	plugins := append([]map[string]interface{}{}, cfg.Plugins...)
	for i := 0; i < len(plugins); i++ {
		for j := i + 1; j < len(plugins); j++ {
			ti, _ := plugins[i]["type"].(string)
			tj, _ := plugins[j]["type"].(string)
			if order[ti] > order[tj] {
				plugins[i], plugins[j] = plugins[j], plugins[i]
			}
		}
	}

	// Convert typed users → []map so plugins' type asserts always work after JSON round-trips too.
	usersMaps := usersToMaps(cfg.Users)

	var firstErr error
	okCount := 0
	okByType := map[string]bool{}
	for _, p := range plugins {
		typ, _ := p["type"].(string)
		pluginCfg, _ := p["config"].(map[string]interface{})
		if pluginCfg == nil {
			pluginCfg = map[string]interface{}{}
		}
		switch typ {
		case "nft_forward":
			rules := make([]interface{}, 0, len(cfg.Forwards))
			for _, f := range cfg.Forwards {
				rules = append(rules, map[string]interface{}{
					"listen_port": f.ListenPort,
					"target_host": f.TargetHost,
					"target_port": f.TargetPort,
					"comment":     f.Comment,
				})
			}
			pluginCfg["rules"] = rules
		case "mita_server", "socks_in", "mieru_client":
			pluginCfg["users"] = usersMaps
		}
		pl, ok := a.registry.Get(typ)
		if !ok {
			log.Printf("unknown plugin %s — skip", typ)
			continue
		}
		if err := pl.Apply(ctx, pluginCfg); err != nil {
			// Continue remaining plugins so public listeners (socks_in) still come up
			// even if mita/mieru fail (e.g. download race). Required plugins block version bump.
			log.Printf("plugin %s apply error: %v", typ, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("plugin %s: %w", typ, err)
			}
			continue
		}
		okCount++
		okByType[typ] = true
	}
	// seed counters for known users on exit
	for _, u := range cfg.Users {
		if _, ok := a.counters[u.UserID]; !ok {
			a.counters[u.UserID] = &userCounter{}
		}
	}
	if okCount == 0 && firstErr != nil {
		return firstErr
	}
	// Fail if any required plugin for this role did not succeed → retry same version.
	for _, typ := range required {
		if !okByType[typ] {
			err := firstErr
			if err == nil {
				err = fmt.Errorf("required plugin %s missing or failed", typ)
			}
			log.Printf("partial apply incomplete (need %v, ok=%v): %v", required, okByType, err)
			return fmt.Errorf("partial apply: %w", err)
		}
	}
	if firstErr != nil {
		// optional plugin failed but required ones OK
		log.Printf("partial apply (optional only): %v (ok=%d)", firstErr, okCount)
	}
	return nil
}

// requiredPlugins lists plugin types that must succeed for the role to be healthy.
func requiredPlugins(cfg *model.AgentDesiredConfig) []string {
	// From desired plugins present in config (not just role string — hybrid has both).
	need := map[string]bool{}
	for _, p := range cfg.Plugins {
		typ, _ := p["type"].(string)
		switch typ {
			case "mita_server", "mieru_client", "socks_in", "tcp_forward":
				need[typ] = true
			}
	}
	out := make([]string, 0, len(need))
	for t := range need {
		out = append(out, t)
	}
	return out
}

// usersToMaps converts AgentUser slice to []map[string]interface{} for plugin configs.
// Plugins previously did cfg["users"].([]interface{}) which fails for []model.AgentUser.
func usersToMaps(users []model.AgentUser) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]interface{}{
			"user_id":   u.UserID,
			"username":  u.Username,
			"name":      u.Username,
			"password":  u.Password,
			"enabled":   u.Enabled,
			"mita_user": u.MitaUser,
		})
	}
	return out
}

// reportTraffic sends deltas. MVP uses tiny synthetic idle zeros unless
// external meters call AddBytes. Real deployment hooks mita/socks counters here.
func (a *Agent) reportTraffic(ctx context.Context) error {
	samples := make([]model.TrafficReportSample, 0, len(a.counters))
	now := time.Now().Unix()
	for uid, c := range a.counters {
		// zero deltas by default — placeholder for real meter integration
		samples = append(samples, model.TrafficReportSample{
			UserID:    uid,
			UpDelta:   0,
			DownDelta: 0,
			UpBps:     0,
			DownBps:   0,
			TS:        now,
		})
		_ = c
	}
	if len(samples) == 0 {
		return nil
	}
	body := model.TrafficReport{
		NodeID:  a.cfg.NodeID,
		Token:   a.cfg.Token,
		Samples: samples,
	}
	var resp map[string]interface{}
	return a.postJSON(ctx, "/api/agent/traffic", body, &resp)
}

// AddBytes allows external metering hooks (future socks/mita interceptor).
func (a *Agent) AddBytes(userID int64, up, down int64) {
	c, ok := a.counters[userID]
	if !ok {
		c = &userCounter{}
		a.counters[userID] = c
	}
	c.up += up
	c.down += down
}

func (a *Agent) postJSON(ctx context.Context, path string, body any, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.PanelURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("POST %s -> %d %s", path, res.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
