package workers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	deploymodule "releaseaworker/internal/features/deploys/worker"
	maintenancemodule "releaseaworker/internal/features/maintenance/worker"
	rulesmodule "releaseaworker/internal/features/rules/worker"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	platformshared "releaseaworker/internal/platform/shared"
	"time"
)

type operationExecutor func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error

var ErrUnsupportedOperationType = errors.New("unsupported operation type")

var defaultOperationExecutors = map[string]operationExecutor{
	models.OperationTypeServiceDeploy:        executeServiceDeploy,
	models.OperationTypeServiceScale:         executeServiceScale,
	models.OperationTypeServicePromoteCanary: deploymodule.HandlePromoteCanary,
	models.OperationTypeServiceDelete:        deploymodule.HandleServiceDelete,
	models.OperationTypeRuleDeploy:           rulesmodule.HandleRuleDeploy,
	models.OperationTypeRulePublish:          rulesmodule.HandleRuleDeploy,
	models.OperationTypeRuleDelete:           rulesmodule.HandleRuleDelete,
	models.OperationTypeWorkerRestart:        executeWorkerRestart,
}

func executeOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	executor, ok := defaultOperationExecutors[op.Type]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedOperationType, op.Type)
	}
	return executor(ctx, client, cfg, tokens, op)
}

func executeWorkerRestart(ctx context.Context, _ *http.Client, cfg models.Config, _ *platformauth.TokenManager, op models.OperationPayload) error {
	return platformkube.RestartDeployment(ctx, cfg, op.Payload)
}

func executeServiceDeploy(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	if err := deploymodule.HandleServiceDeploy(ctx, client, cfg, tokens, op); err != nil {
		return err
	}
	environment := platformops.PayloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}
	runtimeSyncCtx, cancelRuntimeSync := context.WithTimeout(ctx, 20*time.Second)
	defer cancelRuntimeSync()
	if err := maintenancemodule.UpdateRuntimeStatuses(runtimeSyncCtx, client, cfg, tokens); err != nil {
		log.Printf("[worker] post-deploy runtime sync failed service=%s env=%s: %v", op.Resource, environment, err)
	}
	return nil
}

func executeServiceScale(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	service, err := rulesmodule.FetchService(ctx, client, cfg, tokens, op.Resource)
	if err != nil {
		return err
	}
	if service.Type == "static-site" {
		return nil
	}
	serviceName := platformshared.ToKubeName(service.Name)
	if serviceName == "" {
		serviceName = platformshared.ToKubeName(service.ID)
	}
	if serviceName == "" {
		return errors.New("service name is required for scaling")
	}
	environment := platformops.PayloadString(op.Payload, "environment")
	if environment == "" {
		environment = cfg.Environment
	}
	if environment == "" {
		environment = "prod"
	}
	namespace := platformshared.ResolveNamespace(cfg, environment)
	targets := deploymodule.ResolveServicePayloadDeploymentTargets(service, serviceName)
	if len(targets) == 0 {
		targets = []string{serviceName}
	}
	replicas := platformops.PayloadInt(op.Payload, "replicas")
	for _, target := range targets {
		if err := platformkube.ScaleDeployment(ctx, namespace, target, replicas); err != nil {
			return fmt.Errorf("scale deployment %s: %w", target, err)
		}
	}
	return nil
}
