package workers

import (
	"context"
	"errors"
	"log"
	"net/http"
	deploymodule "releaseaworker/internal/features/deploys/worker"
	platformauth "releaseaworker/internal/platform/auth"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
	"time"
)

var errOperationConflict = platformops.ErrOperationConflict
var ErrOperationNotCompatible = errors.New("operation not compatible with worker")

type fetchOperationFunc func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, opID string) (models.OperationPayload, error)
type updateOperationStatusFunc func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, opID, status, errMsg string) error
type updateDeployStrategyStatusFunc func(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	deployID string,
	status string,
	strategyType string,
	phase string,
	summary string,
	details map[string]interface{},
) error
type executeOperationFunc func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error
type strategyTypeFunc func(op models.OperationPayload) string
type transientDeployErrorFunc func(err error) bool
type strategyRollbackFunc func(strategyType string) bool
type rollbackDetectedFunc func(err error) bool
type retryDelayFunc func() time.Duration
type retryAttemptsFunc func() int
type waitFunc func(ctx context.Context, duration time.Duration) error

type operationProcessor struct {
	fetchOperation             fetchOperationFunc
	updateOperationStatus      updateOperationStatusFunc
	updateDeployStrategyStatus updateDeployStrategyStatusFunc
	executeOperation           executeOperationFunc
	operationStrategyType      strategyTypeFunc
	isTransientDeployError     transientDeployErrorFunc
	strategyRequiresRollback   strategyRollbackFunc
	rollbackDetected           rollbackDetectedFunc
	deployRetryDelay           retryDelayFunc
	deployRetryMaxAttempts     retryAttemptsFunc
	wait                       waitFunc
}

var defaultOperationProcessor = newDefaultOperationProcessor()

func processOperationByID(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, opID string) error {
	return defaultOperationProcessor.processOperationByID(ctx, client, cfg, tokens, opID)
}

func processOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	return defaultOperationProcessor.processOperation(ctx, client, cfg, tokens, op)
}

func newDefaultOperationProcessor() operationProcessor {
	return operationProcessor{
		fetchOperation:             platformops.FetchOperation,
		updateOperationStatus:      platformops.UpdateOperationStatus,
		updateDeployStrategyStatus: platformops.UpdateDeployStrategyStatus,
		executeOperation:           executeOperation,
		operationStrategyType:      deploymodule.OperationStrategyType,
		isTransientDeployError:     deploymodule.IsTransientDeployError,
		strategyRequiresRollback:   deploymodule.StrategyRequiresRollback,
		rollbackDetected:           deploymodule.IsRollbackPerformedError,
		deployRetryDelay: func() time.Duration {
			seconds := shared.EnvInt("WORKER_DEPLOY_RETRY_DELAY_SECONDS", 6)
			if seconds < 1 {
				seconds = 1
			}
			return time.Duration(seconds) * time.Second
		},
		deployRetryMaxAttempts: func() int {
			attempts := shared.EnvInt("WORKER_DEPLOY_RETRY_MAX_ATTEMPTS", 3)
			if attempts < 1 {
				attempts = 1
			}
			return attempts
		},
		wait: waitWithContext,
	}
}

func waitWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p operationProcessor) processOperationByID(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, opID string) error {
	op, err := p.fetchOperation(ctx, client, cfg, tokens, opID)
	if err != nil {
		return err
	}
	return p.processOperation(ctx, client, cfg, tokens, op)
}

func (p operationProcessor) processOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	if op.Status != models.OperationStatusQueued {
		log.Printf("[worker] operation %s already %s, skipping", op.ID, op.Status)
		return nil
	}

	if compatible, reason := operationCompatibleWithWorker(cfg, op); !compatible {
		log.Printf("[worker] operation %s incompatible with worker %s: %s", op.ID, cfg.WorkerName, reason)
		return ErrOperationNotCompatible
	}

	claimed, err := p.claimOperation(ctx, client, cfg, tokens, op.ID)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}

	log.Printf("[worker] processing operation %s type=%s", op.ID, op.Type)
	execErr := p.executeWithRetry(ctx, client, cfg, tokens, op)

	if execErr != nil {
		return p.failOperation(ctx, client, cfg, tokens, op, execErr)
	}

	if err := p.updateOperationStatus(ctx, client, cfg, tokens, op.ID, models.OperationStatusSucceeded, ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already completed", op.ID)
			return nil
		}
		return err
	}

	log.Printf("[worker] operation %s completed", op.ID)
	return nil
}

func operationCompatibleWithWorker(cfg models.Config, op models.OperationPayload) (bool, string) {
	operationEnvironment := strings.TrimSpace(stringPayload(op.Payload["environment"]))
	if operationEnvironment != "" {
		workerNamespace := shared.ResolveAppNamespace(cfg.Environment)
		operationNamespace := shared.ResolveAppNamespace(operationEnvironment)
		if workerNamespace != operationNamespace {
			return false, "environment namespace mismatch"
		}
	}

	preferredCluster := strings.TrimSpace(stringPayload(op.Payload["preferredWorkerCluster"]))
	if preferredCluster != "" && strings.TrimSpace(cfg.Cluster) != preferredCluster {
		return false, "worker cluster does not match preferred cluster"
	}

	requiredTags := normalizeWorkerTags(payloadStringSlice(op.Payload["workerTags"]))
	if len(requiredTags) == 0 {
		return true, ""
	}

	availableTags := make(map[string]struct{}, len(cfg.Tags))
	for _, tag := range normalizeWorkerTags(cfg.Tags) {
		availableTags[tag] = struct{}{}
	}
	for _, tag := range requiredTags {
		if _, ok := availableTags[tag]; !ok {
			return false, "missing required worker tags"
		}
	}

	return true, ""
}

func normalizeWorkerTags(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func payloadStringSlice(raw interface{}) []string {
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		items := make([]string, 0, len(value))
		for _, item := range value {
			items = append(items, stringPayload(item))
		}
		return items
	default:
		return nil
	}
}

func stringPayload(raw interface{}) string {
	switch value := raw.(type) {
	case string:
		return value
	default:
		return ""
	}
}

func (p operationProcessor) claimOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, operationID string) (bool, error) {
	if err := p.updateOperationStatus(ctx, client, cfg, tokens, operationID, models.OperationStatusInProgress, ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already claimed, skipping", operationID)
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (p operationProcessor) executeWithRetry(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	op models.OperationPayload,
) error {
	maxAttempts := 1
	if p.isRetryableOperation(op) {
		maxAttempts = p.deployRetryMaxAttempts()
	}

	retryDelay := p.deployRetryDelay()
	strategyType := p.operationStrategyType(op)
	var execErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		execErr = p.executeOperation(ctx, client, cfg, tokens, op)
		if execErr == nil {
			return nil
		}
		if !p.shouldRetryOperation(op, execErr, attempt, maxAttempts) {
			return execErr
		}
		p.updateRetryingStatus(ctx, client, cfg, tokens, op, strategyType, attempt, maxAttempts, execErr)
		log.Printf(
			"[worker] %s operation %s transient failure on attempt %d/%d: %v",
			retryOperationLabel(op.Type),
			op.ID,
			attempt,
			maxAttempts,
			execErr,
		)

		if err := p.wait(ctx, retryDelay); err != nil {
			return err
		}
	}

	return execErr
}

func (p operationProcessor) isRetryableOperation(op models.OperationPayload) bool {
	switch op.Type {
	case models.OperationTypeServiceDeploy:
		return op.DeployID != ""
	case models.OperationTypeRuleDeploy, models.OperationTypeRulePublish:
		return op.RuleDeployID != ""
	default:
		return false
	}
}

func (p operationProcessor) shouldRetryOperation(op models.OperationPayload, execErr error, attempt, maxAttempts int) bool {
	if !p.isRetryableOperation(op) {
		return false
	}
	if !p.isTransientDeployError(execErr) {
		return false
	}
	return attempt < maxAttempts
}

func (p operationProcessor) updateRetryingStatus(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	op models.OperationPayload,
	strategyType string,
	attempt int,
	maxAttempts int,
	execErr error,
) {
	if op.Type != models.OperationTypeServiceDeploy || op.DeployID == "" {
		return
	}

	retryDetails := map[string]interface{}{
		"attempt":     attempt,
		"maxAttempts": maxAttempts,
		"error":       execErr.Error(),
	}
	if err := p.updateDeployStrategyStatus(
		ctx,
		client,
		cfg,
		tokens,
		op.DeployID,
		"retrying",
		strategyType,
		"retrying",
		"Temporary instability detected. Retrying deployment",
		retryDetails,
	); err != nil {
		log.Printf("[worker] deploy %s retrying state update failed: %v", op.DeployID, err)
	}
}

func retryOperationLabel(operationType string) string {
	switch operationType {
	case models.OperationTypeServiceDeploy:
		return "deploy"
	case models.OperationTypeRuleDeploy, models.OperationTypeRulePublish:
		return "rule"
	default:
		return "generic"
	}
}

func (p operationProcessor) failOperation(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	op models.OperationPayload,
	execErr error,
) error {
	p.updateDeployFailureStatus(ctx, client, cfg, tokens, op, execErr)

	if err := p.updateOperationStatus(ctx, client, cfg, tokens, op.ID, models.OperationStatusFailed, execErr.Error()); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already completed", op.ID)
			return nil
		}
		return err
	}
	log.Printf("[worker] operation %s failed: %v", op.ID, execErr)
	return nil
}

func (p operationProcessor) updateDeployFailureStatus(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	op models.OperationPayload,
	execErr error,
) {
	if op.Type != models.OperationTypeServiceDeploy || op.DeployID == "" {
		return
	}

	strategyType := p.operationStrategyType(op)
	if p.strategyRequiresRollback(strategyType) && p.rollbackDetected(execErr) {
		rollbackDetails := map[string]interface{}{"error": execErr.Error()}
		if err := p.updateDeployStrategyStatus(
			ctx,
			client,
			cfg,
			tokens,
			op.DeployID,
			"rollback",
			strategyType,
			"rollback",
			rollbackSummary(strategyType),
			rollbackDetails,
		); err != nil {
			log.Printf("[worker] deploy %s rollback state update failed: %v", op.DeployID, err)
		}
		return
	}

	failedDetails := map[string]interface{}{"error": execErr.Error()}
	if err := p.updateDeployStrategyStatus(
		ctx,
		client,
		cfg,
		tokens,
		op.DeployID,
		"failed",
		strategyType,
		"failed",
		"Deployment failed with a permanent inconsistency",
		failedDetails,
	); err != nil {
		log.Printf("[worker] deploy %s failed state update failed: %v", op.DeployID, err)
	}
}

func rollbackSummary(strategyType string) string {
	switch strategyType {
	case "blue-green":
		return "Switching back to the previous active environment"
	case "canary":
		return "Stopping gradual exposure and restoring the previous version"
	default:
		return "Restoring the previous version"
	}
}
