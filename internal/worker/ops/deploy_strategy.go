package ops

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"releaseaworker/internal/worker/common/deploystrategy"
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

func operationStrategyType(op operationPayload) string {
	return normalizeStrategyType(payloadString(op.Payload, "strategyType"))
}

func strategyRequiresRollback(strategyType string) bool {
	normalized := normalizeStrategyType(strategyType)
	return normalized == "canary" || normalized == "blue-green"
}

func isTransientDeployError(err error) bool {
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

func resolveDeployStrategyType(service serviceConfig) string {
	return deploystrategy.NormalizeType(service.DeployTemplateID, service.DeploymentStrategy.Type)
}

func reportDeployStrategyProgress(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	deployID string,
	service serviceConfig,
	status string,
	summary string,
	extraDetails map[string]interface{},
) error {
	if strings.TrimSpace(deployID) == "" {
		return nil
	}
	strategyType := resolveDeployStrategyType(service)
	details := buildDeployStrategyDetails(service)
	for key, value := range extraDetails {
		details[key] = value
	}
	return updateDeployStrategyStatus(ctx, client, cfg, tokens, deployID, status, strategyType, status, summary, details)
}

func buildDeployStrategyDetails(service serviceConfig) map[string]interface{} {
	details := map[string]interface{}{}
	switch resolveDeployStrategyType(service) {
	case "canary":
		canaryPercent := deploystrategy.NormalizeCanaryPercent(service.DeploymentStrategy.CanaryPercent)
		details["exposurePercent"] = canaryPercent
		details["stablePercent"] = 100 - canaryPercent
	case "blue-green":
		primary, secondary := resolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
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

func resolveBlueGreenSlots(primary string) (string, string) {
	return deploystrategy.ResolveBlueGreenSlots(primary)
}
