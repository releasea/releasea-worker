package runtime

import (
	"context"
	"log"
	"net/http"
	"releaseaworker/internal/models"
	deploymodule "releaseaworker/internal/modules/deploy"
	maintenancemodule "releaseaworker/internal/modules/maintenance"
	"releaseaworker/internal/modules/platform"
	rulesmodule "releaseaworker/internal/modules/rules"
	"time"
)

type operationExecutor func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error

var operationExecutors = map[string]operationExecutor{
	"service.deploy":         executeServiceDeploy,
	"service.promote-canary": deploymodule.HandlePromoteCanary,
	"service.delete":         deploymodule.HandleServiceDelete,
	"rule.deploy":            rulesmodule.HandleRuleDeploy,
	"rule.publish":           rulesmodule.HandleRuleDeploy,
	"rule.delete":            rulesmodule.HandleRuleDelete,
	"worker.restart":         executeWorkerRestart,
}

func executeOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	executor, ok := operationExecutors[op.Type]
	if !ok {
		// Preserve previous behavior for unknown operation types.
		time.Sleep(1 * time.Second)
		return nil
	}
	return executor(ctx, client, cfg, tokens, op)
}

func executeWorkerRestart(ctx context.Context, _ *http.Client, cfg models.Config, _ *platform.TokenManager, op models.OperationPayload) error {
	return platform.RestartDeployment(ctx, cfg, op.Payload)
}

func executeServiceDeploy(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	if err := deploymodule.HandleServiceDeploy(ctx, client, cfg, tokens, op); err != nil {
		return err
	}
	environment := platform.PayloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}
	runtimeSyncCtx, cancelRuntimeSync := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelRuntimeSync()
	if err := maintenancemodule.UpdateRuntimeStatuses(runtimeSyncCtx, client, cfg, tokens); err != nil {
		log.Printf("[worker] post-deploy runtime sync failed service=%s env=%s: %v", op.Resource, environment, err)
	}
	return nil
}
