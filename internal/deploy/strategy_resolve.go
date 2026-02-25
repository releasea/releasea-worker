package deploy

import (
	"context"
	"errors"
	"net"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"

	deploystrategy "releaseaworker/internal/shared"
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

func ResolveBlueGreenSlots(primary string) (string, string) {
	return deploystrategy.ResolveBlueGreenSlots(primary)
}
