package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/techeer-sv/swarm-autoscaler/internal/config"
	"github.com/techeer-sv/swarm-autoscaler/internal/docker"
	"github.com/techeer-sv/swarm-autoscaler/internal/scaler"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	cfg := config.Load()

	if cfg.DryRun {
		logger.Info("running in dry-run mode",
			"services_file", cfg.DryRunFile,
			"cpu_file", cfg.DryRunCPUFile,
		)
	}

	dockerClient, err := docker.New()
	if err != nil {
		logger.Error("failed to create docker client", "error", err)
		os.Exit(1)
	}

	controller := scaler.NewController(dockerClient, cfg, logger)

	logger.Info("starting swarm autoscaler")

	for {
		controller.Reconcile(ctx)
		time.Sleep(cfg.Interval)
	}
}
