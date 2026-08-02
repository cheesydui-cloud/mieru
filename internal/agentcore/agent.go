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
	"github.com/cheesydui-cloud/mieru/internal/plugins/procutil"
	"github.com/cheesydui-cloud/mieru/internal/plugins/socksin"
	"github.com/cheesydui-cloud/mieru/internal/plugins/tcpforward"
)

var errUnauthorized = fmt.Errorf("unauthorized: node deleted or token invalid")

// AgentVersion is the default when main does not inject a build version.
// Prefer SetVersion() from cmd/agent so -version and heartbeat always match.
var AgentVersion = "0.4.22"

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
	// per-user metering from `mita get users` (exit/hybrid)
	// meterMu guards counters + userByName (pull seed vs 1s traffic ticker).
	meterMu    sync.Mutex
	counters   map[int64]*userCounter
	userByName map[string]int64
	// stateMu guards version + lastApplyMsg (heartbeat vs background apply).
	stateMu      sync.Mutex
	version      int64
	lastApplyMsg string
	// applyMu serializes pullAndApply so heartbeat never blocks on plugin apply.
	applyMu   chan struct{}
	applyBusy int32 // atomic: 1 while apply running
	// upgradeBusy: 1 while self-upgrade download/install running
	upgradeBusy      int32
	lastUpgradeJobID string // avoid re-running same job every heartbeat
	// traffic log rate-limit (unix sec of last diagnostic line)
	lastTrafficLog int64
	// meterEnabled: true when this node runs mita_server (from desired plugins),
	// not from AGENT_ROLE env — wrong install role used to disable all metering.
	meterEnabled bool
}

// userCounter tracks absolute totals from mita and derived bps.
type userCounter struct {
	lastUp   int64 // last seen cumulative upload bytes (1-day window)
	lastDown int64 // last seen cumulative download bytes
	upBps    int64
	downBps  int64
	lastTS   int64
	primed   bool // false until first sample (no delta yet)
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
		cfg:        cfg,
		client:     &http.Client{Timeout: 15 * time.Second},
		registry:   reg,
		counters:   map[int64]*userCounter{},
		userByName: map[string]int64{},
		applyMu:    make(chan struct{}, 1),
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
	// Restore user→id map from last desired.json so traffic metering works
	// immediately after restart (before / while first pull skips full apply).
	if b, err := os.ReadFile(filepath.Join(a.cfg.DataDir, "desired.json")); err == nil {
		var last model.AgentDesiredConfig
		if json.Unmarshal(b, &last) == nil {
			if len(last.Users) > 0 {
				a.seedMeteringUsers(last.Users)
			}
			a.updateMeteringFromDesired(&last)
			if r := strings.TrimSpace(last.Role); r != "" {
				a.cfg.Role = r
			}
			a.meterMu.Lock()
			n := len(a.userByName)
			en := a.meterEnabled
			a.meterMu.Unlock()
			log.Printf("restored metering map users=%d enabled=%v role=%s from desired.json", n, en, a.cfg.Role)
		}
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
	// 1s traffic sample → panel realtime rates with minimal lag
	traffic := time.NewTicker(1 * time.Second)
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
			if a.shouldMeterTraffic() {
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
		UpgradeJob *upgradeJob `json:"upgrade_job"`
	}
	if err := a.postJSON(ctx, "/api/agent/heartbeat", body, &resp); err != nil {
		if err == errUnauthorized {
			log.Printf("heartbeat 401 — node deleted or token revoked; stopping all services")
			a.stopAllPlugins()
		}
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
	// Panel-pushed self-upgrade (download tarball + restart). Non-blocking.
	if resp.UpgradeJob != nil && resp.UpgradeJob.ID != "" {
		a.scheduleUpgrade(ctx, *resp.UpgradeJob)
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
	if res.StatusCode == http.StatusUnauthorized {
		log.Printf("config pull 401 — node deleted or token revoked; stopping all services")
		a.stopAllPlugins()
		return errUnauthorized
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

	// Skip full re-apply when desired version is already live.
	// Re-applying tcp_forward/mita every pull (default 10s) tears down active
	// phone sessions — classic "works ~30s then dies" symptom.
	//
	// Still seed metering maps every pull: after agent restart, version is
	// restored from disk but userByName is empty — without this, traffic
	// samples from `mita get users` never match panel user IDs (all zeros).
	a.seedMeteringUsers(cfg.Users)
	a.updateMeteringFromDesired(&cfg)
	// Keep local role in sync with panel desired config (env may be wrong/stale).
	if r := strings.TrimSpace(cfg.Role); r != "" {
		a.cfg.Role = r
	}
	a.stateMu.Lock()
	cur := a.version
	needRetry := a.lastApplyMsg != ""
	a.stateMu.Unlock()
	if cfg.Version > 0 && cfg.Version == cur && !needRetry {
		return nil
	}

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

// seedMeteringUsers rebuilds username→user_id for traffic reporting without
// touching plugins. Safe to call on every pull (idempotent).
func (a *Agent) seedMeteringUsers(users []model.AgentUser) {
	// Always replace so revoked users stop matching; empty → clear map.
	next := map[string]int64{}
	a.meterMu.Lock()
	defer a.meterMu.Unlock()
	for _, u := range users {
		if u.UserID <= 0 {
			continue
		}
		if _, ok := a.counters[u.UserID]; !ok {
			a.counters[u.UserID] = &userCounter{}
		}
		name := strings.TrimSpace(u.MitaUser)
		if name == "" {
			name = strings.TrimSpace(u.Username)
		}
		if name != "" {
			next[name] = u.UserID
			low := strings.ToLower(name)
			if low != name {
				next[low] = u.UserID
			}
		}
	}
	a.userByName = next
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
	plist := append([]map[string]interface{}{}, cfg.Plugins...)
	for i := 0; i < len(plist); i++ {
		for j := i + 1; j < len(plist); j++ {
			ti, _ := plist[i]["type"].(string)
			tj, _ := plist[j]["type"].(string)
			if order[ti] > order[tj] {
				plist[i], plist[j] = plist[j], plist[i]
			}
		}
	}

	// Stop plugins that are no longer in desired config (e.g. last tunnel deleted
	// → tcp_forward/socks_in must release ports; otherwise "node still has network").
	desiredTypes := map[string]bool{}
	for _, p := range plist {
		if typ, _ := p["type"].(string); typ != "" {
			desiredTypes[typ] = true
		}
	}
	for _, pl := range a.registry.All() {
		name := pl.Name()
		if desiredTypes[name] {
			continue
		}
		// Public listeners always stop when removed from desired.
		// mita/mieru only when desired is completely empty (node revoked / no role plugins).
		if name == "mita_server" || name == "mieru_client" {
			if len(desiredTypes) > 0 {
				continue
			}
		} else if name != "tcp_forward" && name != "socks_in" && name != "nft_forward" {
			continue
		}
		if stopper, ok := pl.(plugins.Stopper); ok {
			log.Printf("stopping plugin %s (not in desired)", name)
			stopper.Stop()
		}
	}

	// Convert typed users → []map so plugins' type asserts always work after JSON round-trips too.
	usersMaps := usersToMaps(cfg.Users)

	var firstErr error
	okCount := 0
	okByType := map[string]bool{}
	// Empty desired plugins is valid (front with zero tunnels) — already stopped above.
	if len(plist) == 0 {
		log.Printf("apply: no plugins in desired config (listeners stopped if any)")
		return nil
	}
	for _, p := range plist {
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
	// seed counters + username map for mita metering
	a.seedMeteringUsers(cfg.Users)
	a.updateMeteringFromDesired(cfg)
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

// reportTraffic samples `mita get users` on exit/hybrid, derives per-user bps
// from 1-second deltas of 1DayDownload/1DayUpload, and posts to the panel.
func (a *Agent) reportTraffic(ctx context.Context) error {
	now := time.Now().Unix()
	totals, err := a.readMitaUserTotals()
	if err != nil {
		a.trafficLogf("traffic: mita sample failed: %v", err)
		// mita not ready / no users yet — report zeros for known users so UI clears stale rates
		return a.postTrafficSamples(ctx, a.zeroSamples(now))
	}

	a.meterMu.Lock()
	samples := make([]model.TrafficReportSample, 0, len(totals)+len(a.counters))
	skipped := 0
	var matched int
	var sumUpDelta, sumDownDelta, sumUpBps, sumDownBps int64
	mapSize := len(a.userByName)
	for name, tot := range totals {
		uid, ok := a.userByName[name]
		if !ok || uid <= 0 {
			// case-insensitive fallback (mita usernames are usually exact)
			uid, ok = a.userByName[strings.ToLower(name)]
		}
		if !ok || uid <= 0 {
			skipped++
			continue
		}
		c, ok := a.counters[uid]
		if !ok {
			c = &userCounter{}
			a.counters[uid] = c
		}

		var upDelta, downDelta int64
		if !c.primed {
			// first sample: establish baseline, no spike
			c.primed = true
			c.lastUp = tot.up
			c.lastDown = tot.down
			c.lastTS = now
			c.upBps = 0
			c.downBps = 0
		} else {
			elapsed := now - c.lastTS
			if elapsed <= 0 {
				elapsed = 1
			}
			// 1-day counters can reset/slide — treat decrease as new baseline
			if tot.up >= c.lastUp {
				upDelta = tot.up - c.lastUp
			} else {
				upDelta = 0
				c.lastUp = tot.up
			}
			if tot.down >= c.lastDown {
				downDelta = tot.down - c.lastDown
			} else {
				downDelta = 0
				c.lastDown = tot.down
			}
			// bytes/sec * 8 = bits/sec
			c.upBps = (upDelta * 8) / elapsed
			c.downBps = (downDelta * 8) / elapsed
			c.lastUp = tot.up
			c.lastDown = tot.down
			c.lastTS = now
		}
		matched++
		sumUpDelta += upDelta
		sumDownDelta += downDelta
		sumUpBps += c.upBps
		sumDownBps += c.downBps
		samples = append(samples, model.TrafficReportSample{
			UserID:    uid,
			UpDelta:   upDelta,
			DownDelta: downDelta,
			UpBps:     c.upBps,
			DownBps:   c.downBps,
			TS:        now,
		})
	}
	// users with no mita line this tick → 0 rate
	seen := map[int64]bool{}
	for _, s := range samples {
		seen[s.UserID] = true
	}
	for uid := range a.counters {
		if seen[uid] {
			continue
		}
		samples = append(samples, model.TrafficReportSample{
			UserID: uid, UpBps: 0, DownBps: 0, TS: now,
		})
	}
	a.meterMu.Unlock()

	if skipped > 0 && mapSize == 0 {
		a.trafficLogf("traffic: mita has %d user line(s) but agent user map empty — wait for config pull", len(totals))
	} else if skipped > 0 {
		a.trafficLogf("traffic: skipped %d mita user(s) not in panel map (names=%v map=%d)", skipped, keysOf(totals), mapSize)
	} else if matched > 0 && (sumUpDelta > 0 || sumDownDelta > 0) {
		// only log when there is real progress (rate-limited inside trafficLogf)
		a.trafficLogf("traffic: ok matched=%d mita_rows=%d map=%d Δup=%d Δdown=%d up_bps=%d down_bps=%d",
			matched, len(totals), mapSize, sumUpDelta, sumDownDelta, sumUpBps, sumDownBps)
	} else if matched == 0 && len(totals) == 0 {
		a.trafficLogf("traffic: mita get users returned 0 rows (map=%d) — users idle or metrics empty", mapSize)
	}
	if len(samples) == 0 {
		return nil
	}
	if err := a.postTrafficSamples(ctx, samples); err != nil {
		a.trafficLogf("traffic: post failed: %v", err)
		return err
	}
	return nil
}

// trafficLogf rate-limits diagnostic lines to ~once per 30s so journal stays readable.
func (a *Agent) trafficLogf(format string, args ...interface{}) {
	now := time.Now().Unix()
	a.meterMu.Lock()
	if now-a.lastTrafficLog < 30 {
		a.meterMu.Unlock()
		return
	}
	a.lastTrafficLog = now
	a.meterMu.Unlock()
	log.Printf(format, args...)
}

func keysOf(m map[string]mitaUserTotal) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

type mitaUserTotal struct {
	up, down int64 // cumulative 1-day bytes
}

func (a *Agent) postTrafficSamples(ctx context.Context, samples []model.TrafficReportSample) error {
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

func (a *Agent) zeroSamples(now int64) []model.TrafficReportSample {
	a.meterMu.Lock()
	defer a.meterMu.Unlock()
	out := make([]model.TrafficReportSample, 0, len(a.counters))
	for uid := range a.counters {
		out = append(out, model.TrafficReportSample{UserID: uid, TS: now})
	}
	return out
}

// shouldMeterTraffic reports whether this agent should sample mita traffic.
// Uses desired plugins (mita_server present) first; falls back to role string.
func (a *Agent) shouldMeterTraffic() bool {
	a.meterMu.Lock()
	enabled := a.meterEnabled
	a.meterMu.Unlock()
	if enabled {
		return true
	}
	role := strings.TrimSpace(a.cfg.Role)
	return role == model.RoleExit || role == model.RoleHybrid
}

// updateMeteringFromDesired enables traffic sampling when desired config has mita_server
// or is an exit/hybrid role. Front-only agents stay disabled.
func (a *Agent) updateMeteringFromDesired(cfg *model.AgentDesiredConfig) {
	if cfg == nil {
		return
	}
	hasMita := false
	for _, p := range cfg.Plugins {
		typ, _ := p["type"].(string)
		if typ == "mita_server" {
			hasMita = true
			break
		}
	}
	role := strings.TrimSpace(cfg.Role)
	enable := hasMita || role == model.RoleExit || role == model.RoleHybrid
	a.meterMu.Lock()
	prev := a.meterEnabled
	a.meterEnabled = enable
	a.meterMu.Unlock()
	if enable != prev {
		log.Printf("traffic metering enabled=%v (role=%q mita_plugin=%v users=%d)", enable, role, hasMita, len(cfg.Users))
	}
}

// mitaRuntime returns a live mita CLI runtime (same UDS as mita_server plugin).
func (a *Agent) mitaRuntime() (*procutil.MitaRuntime, string, error) {
	dataDir := filepath.Join(a.cfg.DataDir, "mita")
	binDir := filepath.Join(a.cfg.DataDir, "bin")
	bin := filepath.Join(binDir, "mita")
	if st, err := os.Stat(bin); err != nil || st.IsDir() {
		bin = procutil.LookPath("mita")
	}
	if bin == "" {
		return nil, "", fmt.Errorf("mita binary not found (looked in %s and PATH)", binDir)
	}
	rt, err := procutil.EnsureMitaDaemon(bin, dataDir)
	if err != nil {
		return nil, bin, err
	}
	return rt, bin, nil
}

// readMitaUserTotals returns cumulative upload/download bytes per mita username.
// Prefer `mita get metrics` JSON absolute counters (DownloadBytes/UploadBytes);
// fall back to `mita get users` 1-day table if metrics JSON has no user section.
func (a *Agent) readMitaUserTotals() (map[string]mitaUserTotal, error) {
	rt, bin, err := a.mitaRuntime()
	if err != nil {
		return nil, err
	}

	// 1) Absolute counters from metrics JSON — best for cumulative traffic + bps.
	if out, err := rt.MitaCmd("get", "metrics"); err == nil {
		if m := parseMitaMetricsUsersJSON(out); len(m) > 0 {
			return m, nil
		}
		// metrics ok but no users section yet (idle) — still try get users for names
	} else {
		a.trafficLogf("traffic: mita get metrics: %v (bin=%s uds=%s)", err, bin, rt.UDSPath)
	}

	// 2) Fallback: human table (1-day windows). Works even when metrics JSON empty.
	out, err := rt.MitaCmd("get", "users")
	if err != nil {
		return nil, fmt.Errorf("mita get users: %w (bin=%s uds=%s)", err, bin, rt.UDSPath)
	}
	m := parseMitaUsersTable(out)
	if len(m) == 0 && strings.TrimSpace(out) != "" {
		sample := strings.TrimSpace(out)
		if len(sample) > 240 {
			sample = sample[:240] + "…"
		}
		a.trafficLogf("traffic: mita get users parsed 0 rows; raw sample: %q", sample)
	}
	return m, nil
}

// parseMitaMetricsUsersJSON parses `mita get metrics` output:
//
//	{ "users": { "alice": { "DownloadBytes": 123, "UploadBytes": 45 }, ... }, ... }
//
// Values are absolute lifetime counters (not 1-day windows).
func parseMitaMetricsUsersJSON(out string) map[string]mitaUserTotal {
	res := map[string]mitaUserTotal{}
	out = strings.TrimSpace(out)
	if out == "" {
		return res
	}
	// CLI may prepend noise; locate JSON object.
	i := strings.Index(out, "{")
	j := strings.LastIndex(out, "}")
	if i < 0 || j <= i {
		return res
	}
	raw := out[i : j+1]
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &root); err != nil {
		return res
	}
	usersRaw, ok := root["users"]
	if !ok || len(usersRaw) == 0 {
		return res
	}
	var users map[string]map[string]interface{}
	if err := json.Unmarshal(usersRaw, &users); err != nil {
		return res
	}
	for name, fields := range users {
		name = strings.TrimSpace(name)
		if name == "" || fields == nil {
			continue
		}
		down := jsonInt64(fields, "DownloadBytes", "download_bytes", "downloadBytes")
		up := jsonInt64(fields, "UploadBytes", "upload_bytes", "uploadBytes")
		res[name] = mitaUserTotal{up: up, down: down}
	}
	return res
}

func jsonInt64(m map[string]interface{}, keys ...string) int64 {
	for _, k := range keys {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case float64:
			return int64(t)
		case int64:
			return t
		case int:
			return int64(t)
		case json.Number:
			n, _ := t.Int64()
			return n
		case string:
			n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
			return n
		}
	}
	return 0
}

// parseMitaUsersTable parses:
//
//	User  LastActive  1DayDownload  1DayUpload  30DaysDownload  30DaysUpload
//	abcd  2025-...    938.1MiB      12.9MiB     4.0GiB          31.8MiB
func parseMitaUsersTable(out string) map[string]mitaUserTotal {
	res := map[string]mitaUserTotal{}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if strings.HasPrefix(low, "user") && strings.Contains(low, "lastactive") {
			continue
		}
		if strings.HasPrefix(line, "---") {
			continue
		}
		fields := strings.Fields(line)
		// need at least: name + sizes; never-active may be "user never 0B 0B ..." or "user - 0B 0B"
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		// skip if name looks like a header remnant
		if strings.EqualFold(name, "user") {
			continue
		}
		// columns: User LastActive 1DayDownload 1DayUpload ...
		// Find first two size-like tokens after name (LastActive is not a size).
		downStr, upStr := "", ""
		sizes := make([]string, 0, 4)
		for _, f := range fields[1:] {
			if looksLikeSize(f) {
				sizes = append(sizes, f)
			}
		}
		if len(sizes) >= 2 {
			downStr, upStr = sizes[0], sizes[1]
		} else if len(fields) >= 4 {
			// fallback: classic layout fields[2], fields[3]
			downStr, upStr = fields[2], fields[3]
		} else {
			continue
		}
		down := parseHumanSize(downStr)
		up := parseHumanSize(upStr)
		res[name] = mitaUserTotal{up: up, down: down}
	}
	return res
}

func looksLikeSize(s string) bool {
	if s == "" {
		return false
	}
	c := s[len(s)-1]
	return (c >= '0' && c <= '9') || c == 'B' || c == 'b'
}

// parseHumanSize parses values like 938.1MiB, 4.0GiB, 12.9MB, 100B.
func parseHumanSize(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	// split number and unit
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			i++
			continue
		}
		break
	}
	if i == 0 {
		return 0
	}
	numStr := s[:i]
	unit := strings.ToLower(strings.TrimSpace(s[i:]))
	f, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}
	var mul float64 = 1
	switch unit {
	case "", "b", "byte", "bytes":
		mul = 1
	case "k", "kb", "kib":
		mul = 1024
	case "m", "mb", "mib":
		mul = 1024 * 1024
	case "g", "gb", "gib":
		mul = 1024 * 1024 * 1024
	case "t", "tb", "tib":
		mul = 1024 * 1024 * 1024 * 1024
	default:
		// unknown unit
		mul = 1
	}
	if f < 0 {
		return 0
	}
	return int64(f * mul)
}

// AddBytes allows external metering hooks (optional).
func (a *Agent) AddBytes(userID int64, up, down int64) {
	a.meterMu.Lock()
	defer a.meterMu.Unlock()
	c, ok := a.counters[userID]
	if !ok {
		c = &userCounter{}
		a.counters[userID] = c
	}
	// treat as absolute bump on last totals for next delta
	c.lastUp += up
	c.lastDown += down
}

// stopAllPlugins halts every registered Stopper (node deleted / token revoked).
func (a *Agent) stopAllPlugins() {
	for _, pl := range a.registry.All() {
		if stopper, ok := pl.(plugins.Stopper); ok {
			log.Printf("stopping plugin %s (node deactivated)", pl.Name())
			stopper.Stop()
		}
	}
	a.stateMu.Lock()
	a.version = 0
	a.lastApplyMsg = "node deleted or token revoked"
	a.stateMu.Unlock()
	_ = os.WriteFile(filepath.Join(a.cfg.DataDir, "version"), []byte("0"), 0o644)
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
	if res.StatusCode == http.StatusUnauthorized {
		return errUnauthorized
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("POST %s -> %d %s", path, res.StatusCode, string(data))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}
