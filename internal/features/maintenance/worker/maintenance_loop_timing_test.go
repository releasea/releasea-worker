package maintenance

import (
	"testing"
	"time"
)

func TestResolveMaintenanceCycleTimeoutDerived(t *testing.T) {
	if got := resolveMaintenanceCycleTimeout(40 * time.Second); got != 30*time.Second {
		t.Fatalf("expected derived timeout 30s, got %s", got)
	}
	if got := resolveMaintenanceCycleTimeout(2 * time.Second); got != minMaintenanceCycleTimeout {
		t.Fatalf("expected min timeout clamp, got %s", got)
	}
	if got := resolveMaintenanceCycleTimeout(5 * time.Minute); got != maxMaintenanceCycleTimeout {
		t.Fatalf("expected max timeout clamp, got %s", got)
	}
}

func TestResolveMaintenanceCycleTimeoutOverride(t *testing.T) {
	t.Setenv("WORKER_MAINTENANCE_CYCLE_TIMEOUT_SECONDS", "1")
	if got := resolveMaintenanceCycleTimeout(30 * time.Second); got != minMaintenanceCycleTimeout {
		t.Fatalf("expected min clamp on override, got %s", got)
	}

	t.Setenv("WORKER_MAINTENANCE_CYCLE_TIMEOUT_SECONDS", "999")
	if got := resolveMaintenanceCycleTimeout(30 * time.Second); got != maxMaintenanceCycleTimeout {
		t.Fatalf("expected max clamp on override, got %s", got)
	}

	t.Setenv("WORKER_MAINTENANCE_CYCLE_TIMEOUT_SECONDS", "45")
	if got := resolveMaintenanceCycleTimeout(30 * time.Second); got != 45*time.Second {
		t.Fatalf("expected explicit override, got %s", got)
	}
}
