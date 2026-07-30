package socksin

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// Plugin writes socks inbound allowlist config for Entry nodes.
type Plugin struct {
	DataDir string
}

func (p *Plugin) Name() string { return "socks_in" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(p.DataDir, "socks-in.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	log.Printf("[socks_in] wrote %s", path)
	return nil
}
