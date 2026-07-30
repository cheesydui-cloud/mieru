package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/api"
	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// set by -ldflags "-X main.Version=v0.1.3"
var Version = "v0.1.3"

func main() {
	resetAdmin := flag.Bool("reset-admin", false, "reset admin password from PANEL_ADMIN_* env and exit")
	flag.Parse()

	cfg := config.LoadPanel()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

	if *resetAdmin {
		if err := st.SetAdminPassword(cfg.AdminUser, cfg.AdminPass); err != nil {
			log.Fatalf("reset admin: %v", err)
		}
		fmt.Printf("admin password reset\n  user: %s\n  pass: %s\n  db:   %s\n", cfg.AdminUser, cfg.AdminPass, cfg.DBPath)
		return
	}

	if err := st.EnsureAdmin(cfg.AdminUser, cfg.AdminPass); err != nil {
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
	r := srv.Router()

	go func() {
		log.Printf("mieru-panel %s listening on %s (db=%s)", Version, cfg.Listen, cfg.DBPath)
		log.Printf("admin user: %s", cfg.AdminUser)
		if err := r.Run(cfg.Listen); err != nil {
			log.Fatal(err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Println("shutting down")
}
