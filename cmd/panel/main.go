package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/api"
	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// set by -ldflags "-X main.Version=v0.1.2"
var Version = "v0.1.2"

func main() {
	cfg := config.LoadPanel()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer st.Close()

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
		log.Printf("default admin user: %s", cfg.AdminUser)
		if err := r.Run(cfg.Listen); err != nil {
			log.Fatal(err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Println("shutting down")
}
