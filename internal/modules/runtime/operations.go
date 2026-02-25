package runtime

import (
	"context"
	"errors"
	"log"
	"net/http"
	"releaseaworker/internal/models"
	deploymodule "releaseaworker/internal/modules/deploy"
	"releaseaworker/internal/modules/platform"
	"releaseaworker/internal/modules/shared"
	"time"
)

var errOperationConflict = platform.ErrOperationConflict

func processOperationByID(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, opID string) error {
	op, err := platform.FetchOperation(ctx, client, cfg, tokens, opID)
	if err != nil {
		return err
	}
	return processOperation(ctx, client, cfg, tokens, op)
}

func processOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	if op.Status != "queued" {
		log.Printf("[worker] operation %s already %s, skipping", op.ID, op.Status)
		return nil
	}

	if err := platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "in-progress", ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already claimed, skipping", op.ID)
			return nil
		}
		return err
	}

	log.Printf("[worker] processing operation %s type=%s", op.ID, op.Type)
	execErr := error(nil)
	maxAttempts := 1
	retryDelaySeconds := shared.EnvInt("WORKER_DEPLOY_RETRY_DELAY_SECONDS", 6)
	if retryDelaySeconds < 1 {
		retryDelaySeconds = 1
	}
	strategyType := deploymodule.OperationStrategyType(op)
	if op.Type == "service.deploy" {
		maxAttempts = shared.EnvInt("WORKER_DEPLOY_RETRY_MAX_ATTEMPTS", 3)
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
		if !deploymodule.IsTransientDeployError(execErr) || attempt >= maxAttempts {
			break
		}
		retryDetails := map[string]interface{}{
			"attempt":     attempt,
			"maxAttempts": maxAttempts,
			"error":       execErr.Error(),
		}
		if err := platform.UpdateDeployStrategyStatus(
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
			if deploymodule.StrategyRequiresRollback(strategyType) {
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
				if err := platform.UpdateDeployStrategyStatus(
					ctx,
					client,
					cfg,
					tokens,
					op.DeployID,
					"rollback",
					strategyType,
					"rollback",
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
				if err := platform.UpdateDeployStrategyStatus(
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
		}
		if err := platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", execErr.Error()); err != nil {
			if errors.Is(err, errOperationConflict) {
				log.Printf("[worker] operation %s already completed", op.ID)
				return nil
			}
			return err
		}
		log.Printf("[worker] operation %s failed: %v", op.ID, execErr)
		return nil
	}

	if err := platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "succeeded", ""); err != nil {
		if errors.Is(err, errOperationConflict) {
			log.Printf("[worker] operation %s already completed", op.ID)
			return nil
		}
		return err
	}

	log.Printf("[worker] operation %s completed", op.ID)
	return nil
}
