package deploy

import (
	"context"
	"errors"
	"net"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"strings"

	deploystrategy "releaseaworker/internal/platform/shared"
)

type rollbackPerformedError struct {
	cause error
}

func (e rollbackPerformedError) Error() string {
	if e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

func (e rollbackPerformedError) Unwrap() error {
	return e.cause
}

func (rollbackPerformedError) RollbackPerformed() bool {
	return true
}

// MarkRollbackPerformed wraps an error to signal that rollback steps were executed before failing.
func MarkRollbackPerformed(err error) error {
	if err == nil {
		return nil
	}
	return rollbackPerformedError{cause: err}
}

// IsRollbackPerformedError returns true when an error indicates rollback was executed.
func IsRollbackPerformedError(err error) bool {
	if err == nil {
		return false
	}
	var rollbackErr interface{ RollbackPerformed() bool }
	return errors.As(err, &rollbackErr) && rollbackErr.RollbackPerformed()
}

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
	return normalizeStrategyType(platformops.PayloadString(op.Payload, "strategyType"))
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
