package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cheesydui-cloud/mieru/internal/agentcore"
	"github.com/cheesydui-cloud/mieru/internal/config"
)

func main() {
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
