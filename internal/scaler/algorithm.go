package scaler

import "math"

// Smooth CPU with EMA
func UpdateEMA(previous, current float64) float64 {
	alpha := 0.3
	return alpha*current + (1-alpha)*previous
}

// CPU-based scaling with min=1 friendly behavior
func ComputeDesiredReplicas(currentReplicas uint64, cpu, target float64, min, max uint64) uint64 {
	if target <= 0 {
		return currentReplicas
	}

	// Hysteresis / deadband
	upper := target * 1.10 // +10% tolerance
	lower := target * 0.60 // -40% tolerance

	if cpu >= lower && cpu <= upper {
		// Within deadband, no scaling
		return currentReplicas
	}

	// Proportional scaling
	raw := float64(currentReplicas) * (cpu / target)
	var desired uint64

	if cpu > upper {
		// Scale up: ceil to avoid staying at 1 replica
		desired = uint64(math.Ceil(raw))
	} else {
		// Scale down: floor
		desired = uint64(math.Floor(raw))
	}

	// Optional: step limits (prevent jumps)
	maxUpStep := uint64(2)
	maxDownStep := uint64(1)

	if desired > currentReplicas {
		if desired-currentReplicas > maxUpStep {
			desired = currentReplicas + maxUpStep
		}
	} else if currentReplicas > desired {
		if currentReplicas-desired > maxDownStep {
			desired = currentReplicas - maxDownStep
		}
	}

	// Clamp to min/max
	if desired < min {
		desired = min
	}
	if desired > max {
		desired = max
	}

	return desired
}
