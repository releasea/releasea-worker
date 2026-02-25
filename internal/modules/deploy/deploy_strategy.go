package deploy

import (
	"context"
	"errors"
	"net"
	"net/http"
	"releaseaworker/internal/models"
	"releaseaworker/internal/modules/platform"
	"strings"

	"releaseaworker/internal/modules/shared/deploystrategy"
)

const (
	deployStatusRequested   = "requested"
	deployStatusScheduled   = "scheduled"
	deployStatusPreparing   = "preparing"
	deployStatusDeploying   = "deploying"
	deployStatusValidating  = "validating"
	deployStatusProgressing = "progressing"
	deployStatusPromoting   = "promoting"
	deployStatusCompleted   = "completed"
	deployStatusRollback    = "rollback"
	deployStatusFailed      = "failed"
	deployStatusRetrying    = "retrying"
)

func normalizeStrategyType(raw string) string {
	return deploystrategy.NormalizeType("", raw)
}

func OperationStrategyType(op models.OperationPayload) string {
	return normalizeStrategyType(platform.PayloadString(op.Payload, "strategyType"))
}

func StrategyRequiresRollback(strategyType string) bool {
	normalized := normalizeStrategyType(strategyType)
	return normalized == "canary" || normalized == "blue-green"
}

func IsTransientDeployError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		if temporaryErr, ok := any(netErr).(interface{ Temporary() bool }); ok && temporaryErr.Temporary() {
			return true
		}
	}
	text := strings.ToLower(err.Error())
	transientMarkers := []string{
		"timeout",
		"timed out",
		"temporary",
		"temporarily",
		"connection reset",
		"connection refused",
		"connection aborted",
		"connection lost",
		"broken pipe",
		"i/o timeout",
		"eof",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
		"too many requests",
		"rate limit",
		"try again",
	}
	for _, marker := range transientMarkers {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func ResolveDeployStrategyType(service models.ServiceConfig) string {
	return deploystrategy.NormalizeType(service.DeployTemplateID, service.DeploymentStrategy.Type)
}

func reportDeployStrategyProgress(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platform.TokenManager,
	deployID string,
	service models.ServiceConfig,
	status string,
	summary string,
	extraDetails map[string]interface{},
) error {
	if strings.TrimSpace(deployID) == "" {
		return nil
	}
	strategyType := ResolveDeployStrategyType(service)
	details := buildDeployStrategyDetails(service)
	for key, value := range extraDetails {
		details[key] = value
	}
	return platform.UpdateDeployStrategyStatus(ctx, client, cfg, tokens, deployID, status, strategyType, status, summary, details)
}

func buildDeployStrategyDetails(service models.ServiceConfig) map[string]interface{} {
	details := map[string]interface{}{}
	switch ResolveDeployStrategyType(service) {
	case "canary":
		canaryPercent := deploystrategy.NormalizeCanaryPercent(service.DeploymentStrategy.CanaryPercent)
		details["exposurePercent"] = canaryPercent
		details["stablePercent"] = 100 - canaryPercent
	case "blue-green":
		primary, secondary := ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
		details["activeSlot"] = primary
		details["inactiveSlot"] = secondary
	default:
		targetReplicas := service.MinReplicas
		if targetReplicas < 1 {
			targetReplicas = service.Replicas
		}
		if targetReplicas < 1 {
			targetReplicas = 1
		}
		minReplicas := service.MinReplicas
		if minReplicas < 1 {
			minReplicas = 1
		}
		details["targetReplicas"] = targetReplicas
		details["minReplicas"] = minReplicas
		if service.MaxReplicas > 0 {
			details["maxReplicas"] = service.MaxReplicas
		}
	}
	return details
}

func ResolveBlueGreenSlots(primary string) (string, string) {
	return deploystrategy.ResolveBlueGreenSlots(primary)
}
