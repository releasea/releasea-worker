package platform

import (
	"releaseaworker/internal/models"
	"testing"
)

func TestSanitizeDeployLogLine(t *testing.T) {
	line := "kubectl applying manifests for Kubernetes pods rollout readiness"
	got := sanitizeDeployLogLine(line)
	if got == line {
		t.Fatalf("expected sanitized log line, got unchanged")
	}
}

func TestBuildDeployStrategyDetails(t *testing.T) {
	canaryDetails := buildDeployStrategyDetails(models.ServiceConfig{
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type:          "canary",
			CanaryPercent: 20,
		},
	})
	if canaryDetails["exposurePercent"] != 20 || canaryDetails["stablePercent"] != 80 {
		t.Fatalf("unexpected canary details: %#v", canaryDetails)
	}

	blueGreenDetails := buildDeployStrategyDetails(models.ServiceConfig{
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type:             "blue-green",
			BlueGreenPrimary: "green",
		},
	})
	if blueGreenDetails["activeSlot"] != "green" || blueGreenDetails["inactiveSlot"] != "blue" {
		t.Fatalf("unexpected blue-green details: %#v", blueGreenDetails)
	}

	rollingDetails := buildDeployStrategyDetails(models.ServiceConfig{
		MinReplicas: 2,
		MaxReplicas: 5,
	})
	if rollingDetails["targetReplicas"] != 2 || rollingDetails["minReplicas"] != 2 || rollingDetails["maxReplicas"] != 5 {
		t.Fatalf("unexpected rolling details: %#v", rollingDetails)
	}
}
