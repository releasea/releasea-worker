package workers

import (
	"testing"

	"releaseaworker/internal/platform/models"
)

func TestNextDiscoveredWorkloadHeartbeatUsesDeltaMode(t *testing.T) {
	state := &heartbeatDeltaState{}
	workloads := []models.DiscoveredWorkload{
		{Kind: "Deployment", Name: "api"},
	}

	hash, include, mode, err := nextDiscoveredWorkloadHeartbeat(workloads, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash == "" {
		t.Fatalf("expected hash")
	}
	if !include || mode != "full" {
		t.Fatalf("expected first heartbeat to send full payload, got include=%v mode=%q", include, mode)
	}

	hashAgain, includeAgain, modeAgain, err := nextDiscoveredWorkloadHeartbeat(workloads, state)
	if err != nil {
		t.Fatalf("unexpected error on second heartbeat: %v", err)
	}
	if hashAgain != hash {
		t.Fatalf("expected stable hash, got %q want %q", hashAgain, hash)
	}
	if includeAgain || modeAgain != "unchanged" {
		t.Fatalf("expected unchanged delta mode, got include=%v mode=%q", includeAgain, modeAgain)
	}
}

func TestNextDiscoveredWorkloadHeartbeatResendsWhenInventoryChanges(t *testing.T) {
	state := &heartbeatDeltaState{}
	first := []models.DiscoveredWorkload{{Kind: "Deployment", Name: "api"}}
	second := []models.DiscoveredWorkload{{Kind: "Deployment", Name: "api"}, {Kind: "CronJob", Name: "nightly"}}

	if _, _, _, err := nextDiscoveredWorkloadHeartbeat(first, state); err != nil {
		t.Fatalf("first heartbeat failed: %v", err)
	}
	_, include, mode, err := nextDiscoveredWorkloadHeartbeat(second, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !include || mode != "full" {
		t.Fatalf("expected changed inventory to send full payload again, got include=%v mode=%q", include, mode)
	}
}
