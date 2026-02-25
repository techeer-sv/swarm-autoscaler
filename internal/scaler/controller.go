package scaler

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"
	"github.com/techeer-sv/swarm-autoscaler/internal/config"
)

type Controller struct {
	cli        *client.Client
	state      *StateStore
	cfg        config.Config
	log        *slog.Logger
	dryRunTick uint64
}

func NewController(cli *client.Client, cfg config.Config, logger *slog.Logger) *Controller {
	return &Controller{
		cli:   cli,
		state: NewStateStore(),
		cfg:   cfg,
		log:   logger,
	}
}

func (c *Controller) Reconcile(ctx context.Context) {
	var services []swarm.Service
	cpuSeriesByService := map[string][]float64{}
	tick := c.dryRunTick
	if c.cfg.DryRun {
		c.dryRunTick++
	}

	if c.cfg.DryRun {
		// Load mocked services
		file, err := os.Open(c.cfg.DryRunFile)
		if err != nil {
			c.log.ErrorContext(ctx, "failed to open dry-run mock file", "file", c.cfg.DryRunFile, "error", err)
			return
		}
		defer file.Close()

		if err := json.NewDecoder(file).Decode(&services); err != nil {
			c.log.ErrorContext(ctx, "failed to decode dry-run mock file", "file", c.cfg.DryRunFile, "error", err)
			return
		}

		// Load mocked CPU data (per service)
		cpuFile, err := os.Open(c.cfg.DryRunCPUFile)
		if err != nil {
			c.log.ErrorContext(ctx, "failed to open dry-run CPU mock file", "file", c.cfg.DryRunCPUFile, "error", err)
			return
		}
		defer cpuFile.Close()

		var cpuMock struct {
			Series map[string][]float64 `json:"series"`
		}
		if err := json.NewDecoder(cpuFile).Decode(&cpuMock); err != nil {
			c.log.ErrorContext(ctx, "failed to decode dry-run CPU mock file", "file", c.cfg.DryRunCPUFile, "error", err)
			return
		}
		if cpuMock.Series != nil {
			cpuSeriesByService = cpuMock.Series
		}
	} else {
		result, err := c.cli.ServiceList(ctx, client.ServiceListOptions{})
		if err != nil {
			c.log.ErrorContext(ctx, "failed to list services", "error", err)
			return
		}
		services = result.Items
	}

	for _, svc := range services {
		labels := svc.Spec.Labels

		if labels["autoscaler.enable"] != "true" {
			continue
		}

		targetCPU, _ := strconv.ParseFloat(labels["autoscaler.target.cpu"], 64)
		minReplicas, _ := strconv.ParseUint(labels["autoscaler.min"], 10, 64)
		maxReplicas, _ := strconv.ParseUint(labels["autoscaler.max"], 10, 64)

		state := c.getOrCreateState(svc.ID)

		currentReplicas := *svc.Spec.Mode.Replicated.Replicas
		if c.cfg.DryRun && state.SimulatedReplicas != nil {
			currentReplicas = *state.SimulatedReplicas
		}

		// TODO:
		// 1. Fetch task containers
		// 2. Compute average CPU across containers
		// For now, mock or use dry-run CPU
		avgCPU := 50.0
		if c.cfg.DryRun {
			if series, ok := cpuSeriesByService[svc.Spec.Name]; ok && len(series) > 0 {
				avgCPU = series[int(tick)%len(series)]
			}
		}

		state.EMA = UpdateEMA(state.EMA, avgCPU)

		cooldownActive := time.Since(state.LastScaleTime) < 60*time.Second

		desired := ComputeDesiredReplicas(
			currentReplicas,
			state.EMA,
			targetCPU,
			minReplicas,
			maxReplicas,
		)

		if c.cfg.DryRun {
			c.log.InfoContext(
				ctx,
				"dry-run reconcile",
				"tick", tick,
				"service", svc.Spec.Name,
				"avg_cpu", avgCPU,
				"ema_cpu", state.EMA,
				"target_cpu", targetCPU,
				"current_replicas", currentReplicas,
				"desired_replicas", desired,
				"cooldown_active", cooldownActive,
				"min_replicas", minReplicas,
				"max_replicas", maxReplicas,
			)

			if !cooldownActive && desired != currentReplicas {
				// Simulate that we'd scale, so later ticks reflect new replica count and cooldown.
				state.SimulatedReplicas = &desired
				state.LastScaleTime = time.Now()
				c.log.InfoContext(
					ctx,
					"scaling service",
					"service", svc.Spec.Name,
					"current_replicas", currentReplicas,
					"desired_replicas", desired,
				)
			}
			continue
		}

		if desired != currentReplicas {
			c.log.InfoContext(
				ctx,
				"scaling service",
				"service", svc.Spec.Name,
				"current_replicas", currentReplicas,
				"desired_replicas", desired,
			)

			spec := svc.Spec
			spec.Mode.Replicated.Replicas = &desired

			_, err := c.cli.ServiceUpdate(ctx, svc.ID, client.ServiceUpdateOptions{
				Version: svc.Version,
				Spec:    spec,
			})
			if err != nil {
				c.log.ErrorContext(
					ctx,
					"failed to update service",
					"service", svc.Spec.Name,
					"error", err,
				)
				continue
			}

			state.LastScaleTime = time.Now()
		}
	}
}

func (c *Controller) getOrCreateState(id string) *ServiceState {
	if s, ok := c.state.Services[id]; ok {
		return s
	}

	s := &ServiceState{}
	c.state.Services[id] = s
	return s
}
