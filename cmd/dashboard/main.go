// Command dashboard serves TRMNL devices and the admin UI. It wires storage,
// modules, the renderer and the listeners together.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	configPath := flag.String("config", "/config/dashboard.yaml", "path to the YAML config")
	flag.Parse()

	// dashboard healthz [addr]: exits non-zero unless the admin listener is healthy.
	if flag.Arg(0) == "healthz" {
		addr := "127.0.0.1:8081"
		if flag.NArg() > 1 {
			addr = flag.Arg(1)
		}
		if err := healthz(context.Background(), addr); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(*configPath, log); err != nil {
		log.Error("dashboard stopped", "err", err)
		os.Exit(1)
	}
}

// run serves until SIGINT or SIGTERM. It returns rather than exits, so
// pending spans are flushed on every path.
func run(configPath string, log *slog.Logger) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return fmt.Errorf("loading config %s: %w", configPath, err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := setupTracing(ctx)
	if err != nil {
		return fmt.Errorf("setting up tracing: %w", err)
	}
	defer func() {
		// A fresh context: ctx is cancelled by now.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(ctx); err != nil {
			log.Warn("flushing traces", "err", err)
		}
	}()

	svc, err := New(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("starting service: %w", err)
	}
	return svc.Run(ctx)
}
