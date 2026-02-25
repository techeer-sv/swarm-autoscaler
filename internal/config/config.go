package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Interval      time.Duration
	DryRun        bool
	DryRunFile    string
	DryRunCPUFile string
}

func Load() Config {
	dryRunEnv := strings.ToLower(os.Getenv("AUTOSCALER_DRY_RUN"))
	dryRun := dryRunEnv == "1" || dryRunEnv == "true" || dryRunEnv == "yes"

	dryRunFile := os.Getenv("AUTOSCALER_DRY_RUN_FILE")
	if dryRunFile == "" {
		dryRunFile = "testdata/docker-services.json"
	}

	dryRunCPUFile := os.Getenv("AUTOSCALER_DRY_RUN_CPU_FILE")
	if dryRunCPUFile == "" {
		dryRunCPUFile = "testdata/docker-cpu.json"
	}

	return Config{
		Interval:      20 * time.Second,
		DryRun:        dryRun,
		DryRunFile:    dryRunFile,
		DryRunCPUFile: dryRunCPUFile,
	}
}
