package worker

import (
	"context"
	"errors"
	"log"
	"net/http"
	deploymodule "releaseaworker/internal/deploy"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/shared"
	"time"
)

var errOperationConflict = platform.ErrOperationConflict

type fetchOperationFunc func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, opID string) (models.OperationPayload, error)
type updateOperationStatusFunc func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, opID, status, errMsg string) error
type updateDeployStrategyStatusFunc func(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platform.TokenManager,
	deployID string,
	status string,
	strategyType string,
	phase string,
	summary string,
	details map[string]interface{},
) error
type executeOperationFunc func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error
type strategyTypeFunc func(op models.OperationPayload) string
type transientDeployErrorFunc func(err error) bool
type strategyRollbackFunc func(strategyType string) bool
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
	deployRetryDelay           retryDelayFunc
	deployRetryMaxAttempts     retryAttemptsFunc
	wait                       waitFunc
}

var defaultOperationProcessor = newDefaultOperationProcessor()

func processOperationByID(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, opID string) error {
	return defaultOperationProcessor.processOperationByID(ctx, client, cfg, tokens, opID)
}

func processOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	return defaultOperationProcessor.processOperation(ctx, client, cfg, tokens, op)
}

func newDefaultOperationProcessor() operationProcessor {
	return operationProcessor{
		fetchOperation:             platform.FetchOperation,
		updateOperationStatus:      platform.UpdateOperationStatus,
		updateDeployStrategyStatus: platform.UpdateDeployStrategyStatus,
		executeOperation:           executeOperation,
		operationStrategyType:      deploymodule.OperationStrategyType,
		isTransientDeployError:     deploymodule.IsTransientDeployError,
		strategyRequiresRollback:   deploymodule.StrategyRequiresRollback,
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

func (p operationProcessor) processOperationByID(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, opID string) error {
	op, err := p.fetchOperation(ctx, client, cfg, tokens, opID)
	if err != nil {
		return err
	}
	return p.processOperation(ctx, client, cfg, tokens, op)
}

func (p operationProcessor) processOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	if op.Status != "queued" {
		log.Printf("[worker] operation %s already %s, skipping", op.ID, op.Status)
		return nil
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

	if err := p.updateOperationStatus(ctx, client, cfg, tokens, op.ID, "succeeded", ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already completed", op.ID)
			return nil
		}
		return err
	}

	log.Printf("[worker] operation %s completed", op.ID)
	return nil
}

func (p operationProcessor) claimOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, operationID string) (bool, error) {
	if err := p.updateOperationStatus(ctx, client, cfg, tokens, operationID, "in-progress", ""); err != nil {
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
	tokens *platform.TokenManager,
	op models.OperationPayload,
) error {
	maxAttempts := 1
	if op.Type == "service.deploy" {
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
		if !p.shouldRetryDeploy(op, execErr, attempt, maxAttempts) {
			return execErr
		}
		p.updateRetryingStatus(ctx, client, cfg, tokens, op, strategyType, attempt, maxAttempts, execErr)
		log.Printf("[worker] deploy operation %s transient failure on attempt %d/%d: %v", op.ID, attempt, maxAttempts, execErr)

		if err := p.wait(ctx, retryDelay); err != nil {
			return err
		}
	}

	return execErr
}

func (p operationProcessor) shouldRetryDeploy(op models.OperationPayload, execErr error, attempt, maxAttempts int) bool {
	if op.Type != "service.deploy" {
		return false
	}
	if op.DeployID == "" {
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
	tokens *platform.TokenManager,
	op models.OperationPayload,
	strategyType string,
	attempt int,
	maxAttempts int,
	execErr error,
) {
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

func (p operationProcessor) failOperation(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platform.TokenManager,
	op models.OperationPayload,
	execErr error,
) error {
	p.updateDeployFailureStatus(ctx, client, cfg, tokens, op, execErr)

	if err := p.updateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", execErr.Error()); err != nil {
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
	tokens *platform.TokenManager,
	op models.OperationPayload,
	execErr error,
) {
	if op.Type != "service.deploy" || op.DeployID == "" {
		return
	}

	strategyType := p.operationStrategyType(op)
	if p.strategyRequiresRollback(strategyType) {
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
