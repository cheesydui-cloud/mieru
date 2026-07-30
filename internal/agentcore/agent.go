package agentcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/plugins"
	"github.com/cheesydui-cloud/mieru/internal/plugins/mieru"
	"github.com/cheesydui-cloud/mieru/internal/plugins/mita"
	"github.com/cheesydui-cloud/mieru/internal/plugins/nftables"
	"github.com/cheesydui-cloud/mieru/internal/plugins/socksin"
)

const AgentVersion = "0.1.10"

type Agent struct {
	cfg      config.AgentConfig
	client   *http.Client
	registry *plugins.Registry
	version  int64
	// simple local counters for demo metering on exit
	counters map[int64]*userCounter
}

type userCounter struct {
	up, down int64
}

func New(cfg config.AgentConfig) *Agent {
	reg := plugins.NewRegistry()
	data := cfg.DataDir
	reg.Register(&nftables.Plugin{DataDir: filepath.Join(data, "nft"), DryRun: os.Getenv("AGENT_NFT_DRYRUN") != "0"})
	reg.Register(&mieru.Plugin{DataDir: filepath.Join(data, "mieru")})
	reg.Register(&mita.Plugin{DataDir: filepath.Join(data, "mita")})
	reg.Register(&socksin.Plugin{DataDir: filepath.Join(data, "socks")})

	return &Agent{
		cfg:      cfg,
		client:   &http.Client{Timeout: 15 * time.Second},
		registry: reg,
		counters: map[int64]*userCounter{},
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
	log.Printf("agent starting node=%s role=%s panel=%s", a.cfg.NodeID, a.cfg.Role, a.cfg.PanelURL)

	// Immediate heartbeat so panel shows online without waiting for first ticker.
	if err := a.heartbeat(ctx); err != nil {
		log.Printf("initial heartbeat FAILED: %v", err)
		log.Printf("check: 1) panel reachable from this host  2) node_id/token match  3) firewall allows panel :8080")
	} else {
		log.Printf("initial heartbeat OK — panel should show this node online")
	}

	// initial pull (non-fatal)
	if err := a.pullAndApply(ctx); err != nil {
		log.Printf("initial config pull: %v (continuing with last_good if any)", err)
	}

	hb := time.NewTicker(a.cfg.HeartbeatEvery)
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
			if err := a.pullAndApply(ctx); err != nil {
				log.Printf("pull: %v", err)
			}
		case <-traffic.C:
			if a.cfg.Role == model.RoleExit || a.cfg.Role == model.RoleHybrid {
				if err := a.reportTraffic(ctx); err != nil {
					log.Printf("traffic: %v", err)
				}
			}
		}
	}
}

func (a *Agent) heartbeat(ctx context.Context) error {
	body := model.HeartbeatRequest{
		NodeID:        a.cfg.NodeID,
		Token:         a.cfg.Token,
		Role:          a.cfg.Role,
		ConfigVersion: a.version,
		AgentVersion:  AgentVersion,
		Hostname:      os.Getenv("AGENT_HOSTNAME"),
		PublicIP:      os.Getenv("AGENT_PUBLIC_IP"),
	}
	var resp struct {
		OK            bool  `json:"ok"`
		ConfigVersion int64 `json:"config_version"`
		NeedPull      bool  `json:"need_pull"`
	}
	if err := a.postJSON(ctx, "/api/agent/heartbeat", body, &resp); err != nil {
		return err
	}
	if resp.NeedPull {
		return a.pullAndApply(ctx)
	}
	return nil
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

	// persist last_good payload
	_ = os.WriteFile(filepath.Join(a.cfg.DataDir, "desired.json"), raw, 0o600)

	if err := a.apply(ctx, &cfg); err != nil {
		return err
	}
	a.version = cfg.Version
	_ = os.WriteFile(filepath.Join(a.cfg.DataDir, "version"), []byte(fmt.Sprintf("%d", a.version)), 0o644)
	log.Printf("applied config version=%d plugins=%d users=%d", cfg.Version, len(cfg.Plugins), len(cfg.Users))
	return nil
}

func (a *Agent) apply(ctx context.Context, cfg *model.AgentDesiredConfig) error {
	// materialize forwards into nft plugin config
	for _, p := range cfg.Plugins {
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
		case "mita_server":
			pluginCfg["users"] = cfg.Users
		case "socks_in":
			pluginCfg["users"] = cfg.Users
		case "mieru_client":
			pluginCfg["users"] = cfg.Users
		}
		pl, ok := a.registry.Get(typ)
		if !ok {
			log.Printf("unknown plugin %s — skip", typ)
			continue
		}
		if err := pl.Apply(ctx, pluginCfg); err != nil {
			// keep last_good behavior: report error but don't crash loop
			return fmt.Errorf("plugin %s: %w", typ, err)
		}
	}
	// seed counters for known users on exit
	for _, u := range cfg.Users {
		if _, ok := a.counters[u.UserID]; !ok {
			a.counters[u.UserID] = &userCounter{}
		}
	}
	return nil
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
