package docker

import (
	"context"
	"encoding/json"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

func GetContainerCPU(ctx context.Context, cli *client.Client, containerID string) (float64, error) {
	stats, err := cli.ContainerStats(ctx, containerID, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return 0, err
	}
	defer stats.Body.Close()

	var v container.StatsResponse
	if err := json.NewDecoder(stats.Body).Decode(&v); err != nil {
		return 0, err
	}

	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage -
		v.PreCPUStats.CPUUsage.TotalUsage)

	systemDelta := float64(v.CPUStats.SystemUsage -
		v.PreCPUStats.SystemUsage)

	if systemDelta == 0 {
		return 0, nil
	}

	cpuPercent := (cpuDelta / systemDelta) *
		float64(len(v.CPUStats.CPUUsage.PercpuUsage)) * 100

	return cpuPercent, nil
}
