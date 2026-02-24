package operations

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"
)

var errOperationConflict = errors.New("operation conflict")

type operationExecutor func(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error

var operationExecutors = map[string]operationExecutor{
	"service.deploy":         handleServiceDeploy,
	"service.promote-canary": handlePromoteCanary,
	"service.delete":         handleServiceDelete,
	"rule.deploy":            handleRuleDeploy,
	"rule.publish":           handleRuleDeploy,
	"rule.delete":            handleRuleDelete,
	"worker.restart":         executeWorkerRestart,
}

func executeOperation(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	executor, ok := operationExecutors[op.Type]
	if !ok {
		// Preserve previous behavior for unknown operation types.
		time.Sleep(1 * time.Second)
		return nil
	}
	return executor(ctx, client, cfg, tokens, op)
}

func executeWorkerRestart(ctx context.Context, _ *http.Client, cfg Config, _ *tokenManager, op operationPayload) error {
	return restartDeployment(ctx, cfg, op.Payload)
}

func processOperationByID(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, opID string) error {
	op, err := fetchOperation(ctx, client, cfg, tokens, opID)
	if err != nil {
		return err
	}
	return processOperation(ctx, client, cfg, tokens, op)
}

func processOperation(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	if op.Status != "queued" {
		log.Printf("[worker] operation %s already %s, skipping", op.ID, op.Status)
		return nil
	}

	if err := updateOperationStatus(ctx, client, cfg, tokens, op.ID, "in-progress", ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already claimed, skipping", op.ID)
			return nil
		}
		return err
	}

	log.Printf("[worker] processing operation %s type=%s", op.ID, op.Type)
	execErr := error(nil)
	maxAttempts := 1
	retryDelaySeconds := envInt("WORKER_DEPLOY_RETRY_DELAY_SECONDS", 6)
	if retryDelaySeconds < 1 {
		retryDelaySeconds = 1
	}
	strategyType := operationStrategyType(op)
	if op.Type == "service.deploy" {
		maxAttempts = envInt("WORKER_DEPLOY_RETRY_MAX_ATTEMPTS", 3)
		if maxAttempts < 1 {
			maxAttempts = 1
		}
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		execErr = executeOperation(ctx, client, cfg, tokens, op)
		if execErr == nil {
			break
		}
		if op.Type != "service.deploy" || op.DeployID == "" {
			break
		}
		if !isTransientDeployError(execErr) || attempt >= maxAttempts {
			break
		}
		retryDetails := map[string]interface{}{
			"attempt":     attempt,
			"maxAttempts": maxAttempts,
			"error":       execErr.Error(),
		}
		if err := updateDeployStrategyStatus(
			ctx,
			client,
			cfg,
			tokens,
			op.DeployID,
			deployStatusRetrying,
			strategyType,
			deployStatusRetrying,
			"Temporary instability detected. Retrying deployment",
			retryDetails,
		); err != nil {
			log.Printf("[worker] deploy %s retrying state update failed: %v", op.DeployID, err)
		}
		log.Printf("[worker] deploy operation %s transient failure on attempt %d/%d: %v", op.ID, attempt, maxAttempts, execErr)
		timer := time.NewTimer(time.Duration(retryDelaySeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			execErr = ctx.Err()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			execErr = ctx.Err()
			break
		}
	}

	if execErr != nil {
		if op.Type == "service.deploy" && op.DeployID != "" {
			if strategyRequiresRollback(strategyType) {
				summary := "Restoring the previous version"
				if strategyType == "blue-green" {
					summary = "Switching back to the previous active environment"
				} else if strategyType == "canary" {
					summary = "Stopping gradual exposure and restoring the previous version"
				}
				rollbackDetails := map[string]interface{}{}
				if execErr != nil {
					rollbackDetails["error"] = execErr.Error()
				}
				if err := updateDeployStrategyStatus(
					ctx,
					client,
					cfg,
					tokens,
					op.DeployID,
					deployStatusRollback,
					strategyType,
					deployStatusRollback,
					summary,
					rollbackDetails,
				); err != nil {
					log.Printf("[worker] deploy %s rollback state update failed: %v", op.DeployID, err)
				}
			} else {
				failedDetails := map[string]interface{}{}
				if execErr != nil {
					failedDetails["error"] = execErr.Error()
				}
				if err := updateDeployStrategyStatus(
					ctx,
					client,
					cfg,
					tokens,
					op.DeployID,
					deployStatusFailed,
					strategyType,
					deployStatusFailed,
					"Deployment failed with a permanent inconsistency",
					failedDetails,
				); err != nil {
					log.Printf("[worker] deploy %s failed state update failed: %v", op.DeployID, err)
				}
			}
		}
		if err := updateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", execErr.Error()); err != nil {
			if errors.Is(err, errOperationConflict) {
				log.Printf("[worker] operation %s already completed", op.ID)
				return nil
			}
			return err
		}
		log.Printf("[worker] operation %s failed: %v", op.ID, execErr)
		return nil
	}

	if err := updateOperationStatus(ctx, client, cfg, tokens, op.ID, "succeeded", ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already completed", op.ID)
			return nil
		}
		return err
	}

	log.Printf("[worker] operation %s completed", op.ID)
	return nil
}
