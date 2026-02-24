package deployservice

import (
	"context"
	"errors"
	"net"
	"strings"

	"releaseaworker/common/deploystrategy"
	"releaseaworker/models"
)

func NormalizeType(raw string) string {
	return deploystrategy.NormalizeType("", raw)
}

func RequiresRollback(strategyType string) bool {
	normalized := NormalizeType(strategyType)
	return normalized == "canary" || normalized == "blue-green"
}

func IsTransientError(err error) bool {
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

func ResolveType(service models.ServiceConfig) string {
	return deploystrategy.NormalizeType(service.DeployTemplateID, service.DeploymentStrategy.Type)
}

func BuildDetails(service models.ServiceConfig) map[string]interface{} {
	details := map[string]interface{}{}
	switch ResolveType(service) {
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
