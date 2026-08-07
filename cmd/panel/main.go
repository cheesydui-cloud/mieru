package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/api"
	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// set by -ldflags "-X main.Version=v0.5.28"
var Version = "v0.5.28"

func main() {
	resetAdmin := flag.Bool("reset-admin", false, "reset admin password from PANEL_ADMIN_* env and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	cfg := config.LoadPanel()
	if !filepath.IsAbs(cfg.DBPath) {
		log.Printf("WARNING: PANEL_DB is relative (%s) — depends on WorkingDirectory; prefer absolute path", cfg.DBPath)
	}
	if cfg.JWTSecret == "change-me-in-production-please" || cfg.JWTSecret == "change-me-in-production" {
		log.Printf("WARNING: PANEL_JWT_SECRET is default — set a strong secret in /etc/mieru-panel.env")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	// Bootstrap / force-reset: --reset-admin or PANEL_ADMIN_FORCE_SYNC=1
	// Normal start only ensures first admin exists so UI password changes persist.
	forceSync := *resetAdmin || os.Getenv("PANEL_ADMIN_FORCE_SYNC") == "1"
	if forceSync {
		if err := st.SetAdminPassword(cfg.AdminUser, cfg.AdminPass); err != nil {
			log.Fatalf("sync admin: %v", err)
		}
		if *resetAdmin {
			fmt.Printf("admin password reset\n  user: %s\n  pass: %s\n  db:   %s\n", cfg.AdminUser, cfg.AdminPass, cfg.DBPath)
			return
		}
		log.Printf("admin force-synced from env: %s", cfg.AdminUser)
	} else if err := st.EnsureAdmin(cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("ensure admin: %v", err)
	}

	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			_ = st.RefreshUserStatuses()
		}
	}()

	srv := api.New(cfg, st)
	srv.Version = Version
	// After every panel upgrade/restart: rebuild desired configs so agents pull
	// without requiring the operator to click「重建配置」.
	srv.EnsureDesiredConfigs()
	r := srv.Router()

	go func() {
		log.Printf("mieru-panel %s listening on %s (db=%s)", Version, cfg.Listen, cfg.DBPath)
		log.Printf("admin user=%s password=synced-from-env", cfg.AdminUser)
		if err := r.Run(cfg.Listen); err != nil {
			log.Fatal(err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Println("shutting down")
}
