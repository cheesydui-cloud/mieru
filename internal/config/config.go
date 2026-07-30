package config

import (
	"os"
	"strconv"
	"time"
)

type PanelConfig struct {
	Listen      string
	DBPath      string
	JWTSecret   string
	AdminUser   string
	AdminPass   string
	CORSOrigins []string
	DataDir     string
}

type AgentConfig struct {
	PanelURL      string
	NodeID        string
	Token         string
	DataDir       string
	HeartbeatEvery time.Duration
	PullEvery     time.Duration
	Role          string
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getenvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func LoadPanel() PanelConfig {
	return PanelConfig{
		Listen:      getenv("PANEL_LISTEN", ":8080"),
		DBPath:      getenv("PANEL_DB", "data/panel.db"),
		JWTSecret:   getenv("PANEL_JWT_SECRET", "change-me-in-production-please"),
		AdminUser:   getenv("PANEL_ADMIN_USER", "admin"),
		AdminPass:   getenv("PANEL_ADMIN_PASS", "admin123"),
		CORSOrigins: []string{getenv("PANEL_CORS", "*")},
		DataDir:     getenv("PANEL_DATA", "data"),
	}
}

func LoadAgent() AgentConfig {
	return AgentConfig{
		PanelURL:       getenv("AGENT_PANEL_URL", "http://127.0.0.1:8080"),
		NodeID:         getenv("AGENT_NODE_ID", ""),
		Token:          getenv("AGENT_TOKEN", ""),
		DataDir:        getenv("AGENT_DATA", "data/agent"),
		HeartbeatEvery: time.Duration(getenvInt("AGENT_HEARTBEAT_SEC", 15)) * time.Second,
		PullEvery:      time.Duration(getenvInt("AGENT_PULL_SEC", 10)) * time.Second,
		Role:           getenv("AGENT_ROLE", "entry"),
	}
}
