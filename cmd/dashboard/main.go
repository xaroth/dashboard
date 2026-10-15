package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/xaroth/dashboard/service"
)

func main() {
	configPath := flag.String("config", "/config/dashboard.yaml", "path to the YAML config")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Error("loading config", "path", *configPath, "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc, err := service.New(cfg, log)
	if err != nil {
		log.Error("starting service", "err", err)
		os.Exit(1)
	}
	if err := svc.Run(ctx); err != nil {
		log.Error("service stopped", "err", err)
		os.Exit(1)
	}
}
