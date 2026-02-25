package maintenance

import (
	"releaseaworker/internal/platform/shared"
	"time"
)

const (
	minMaintenanceCycleTimeout = 5 * time.Second
	maxMaintenanceCycleTimeout = 2 * time.Minute
)

func resolveMaintenanceCycleTimeout(interval time.Duration) time.Duration {
	overrideSeconds := shared.EnvInt("WORKER_MAINTENANCE_CYCLE_TIMEOUT_SECONDS", 0)
	if overrideSeconds > 0 {
		override := time.Duration(overrideSeconds) * time.Second
		return clampMaintenanceCycleTimeout(override)
	}

	if interval <= 0 {
		return 30 * time.Second
	}

	derived := interval - (interval / 4)
	return clampMaintenanceCycleTimeout(derived)
}

func clampMaintenanceCycleTimeout(value time.Duration) time.Duration {
	if value < minMaintenanceCycleTimeout {
		return minMaintenanceCycleTimeout
	}
	if value > maxMaintenanceCycleTimeout {
		return maxMaintenanceCycleTimeout
	}
	return value
}
