package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kericchhh/rolling-fleet/internal/server"
)

var version = "dev"

const maxDrainDelay = 30 * time.Second

type config struct {
	addr       string
	drainDelay time.Duration
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("fleet-app stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("get hostname: %w", err)
	}
	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api := server.New(server.Info{
		Version:   version,
		Hostname:  hostname,
		StartedAt: time.Now().UTC(),
	}, logger)
	return api.Run(ctx, ln, cfg.drainDelay)
}

func loadConfig() (config, error) {
	cfg := config{addr: ":8080", drainDelay: 10 * time.Second}
	if port := os.Getenv("PORT"); port != "" {
		cfg.addr = ":" + port
	}
	if v := os.Getenv("DRAIN_DELAY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return config{}, fmt.Errorf("parse DRAIN_DELAY: %w", err)
		}
		if d < 0 || d > maxDrainDelay {
			return config{}, fmt.Errorf("DRAIN_DELAY must be between 0s and %s, got %s", maxDrainDelay, d)
		}
		cfg.drainDelay = d
	}
	return cfg, nil
}
