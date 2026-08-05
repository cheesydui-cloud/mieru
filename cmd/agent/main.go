package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cheesydui-cloud/mieru/internal/agentcore"
	"github.com/cheesydui-cloud/mieru/internal/config"
)

// set by -ldflags "-X main.Version=v0.5.25"
var Version = "v0.5.25"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(Version)
		return
	}

	// Keep heartbeat agent_version identical to `mieru-agent -version`.
	agentcore.SetVersion(Version)

	cfg := config.LoadAgent()
	a := agentcore.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		cancel()
	}()

	if err := a.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
