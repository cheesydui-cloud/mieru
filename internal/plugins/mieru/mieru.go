package mieru

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// Plugin writes mieru client config for Relay -> Exit.
type Plugin struct {
	DataDir string
}

func (p *Plugin) Name() string { return "mieru_client" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(p.DataDir, "mieru-client.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	log.Printf("[mieru_client] wrote %s", path)
	return nil
}
