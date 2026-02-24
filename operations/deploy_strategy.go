package operations

import (
	"context"
	"net/http"
	"strings"

	deployservice "releaseaworker/operations/deploy_service"
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
	return deployservice.NormalizeType(raw)
}

func operationStrategyType(op operationPayload) string {
	return normalizeStrategyType(payloadString(op.Payload, "strategyType"))
}

func strategyRequiresRollback(strategyType string) bool {
	return deployservice.RequiresRollback(strategyType)
}

func isTransientDeployError(err error) bool {
	return deployservice.IsTransientError(err)
}

func resolveDeployStrategyType(service serviceConfig) string {
	return deployservice.ResolveType(service)
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
	return deployservice.BuildDetails(service)
}

func resolveBlueGreenSlots(primary string) (string, string) {
	return deployservice.ResolveBlueGreenSlots(primary)
}
