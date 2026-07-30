package mita

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// Plugin writes mita server user list on Exit nodes.
type Plugin struct {
	DataDir string
}

func (p *Plugin) Name() string { return "mita_server" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(p.DataDir, "mita-users.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	log.Printf("[mita_server] wrote %s", path)
	return nil
}
