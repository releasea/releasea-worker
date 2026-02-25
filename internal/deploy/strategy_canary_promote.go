package deploy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/shared"
	"time"
)

func HandlePromoteCanary(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	if op.Resource == "" {
		return errors.New("service id missing")
	}
	environment := platform.PayloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}

	contextData, err := fetchServiceContext(ctx, client, cfg, tokens, op.Resource, environment)
	if err != nil {
		return err
	}
	if ResolveDeployStrategyType(contextData.Service) != "canary" {
		return fmt.Errorf("service is not using canary strategy")
	}

	serviceName := shared.ToKubeName(contextData.Service.Name)
	if serviceName == "" {
		serviceName = shared.ToKubeName(contextData.Service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}
	namespace := shared.ResolveNamespace(cfg, environment)
	kubeClient, kubeToken, err := platform.KubeClient()
	if err != nil {
		return err
	}

	canaryName := serviceName + "-canary"
	canaryDeployment, err := platform.FetchResourceAsMap(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, canaryName)
	if err != nil {
		return fmt.Errorf("canary deployment not found (promote only after canary deploy): %w", err)
	}
	canaryService, err := platform.FetchResourceAsMap(ctx, kubeClient, kubeToken, "v1", "Service", namespace, canaryName)
	if err != nil {
		return fmt.Errorf("canary service not found: %w", err)
	}

	desiredReplicas := resolveDesiredReplicasForPromote(contextData.Service)
	stableDeployment := shared.RenderTemplateResource(canaryDeployment, map[string]string{})
	stableService := shared.RenderTemplateResource(canaryService, map[string]string{})
	if stableDeployment == nil || stableService == nil {
		return errors.New("failed to clone canary resources")
	}
	platform.CleanResourceForReapply(stableDeployment)
	platform.CleanResourceForReapply(stableService)
	RetargetDeployment(stableDeployment, serviceName, serviceName, desiredReplicas)
	stampDeploymentPromotion(stableDeployment)
	RetargetService(stableService, serviceName, serviceName)

	log.Printf("[worker] promote canary: applying stable %s (replicas=%d)", serviceName, desiredReplicas)
	if err := platform.ApplyResource(ctx, kubeClient, kubeToken, stableDeployment); err != nil {
		return fmt.Errorf("apply stable deployment: %w", err)
	}
	if err := platform.ApplyResource(ctx, kubeClient, kubeToken, stableService); err != nil {
		return fmt.Errorf("apply stable service: %w", err)
	}

	if err := waitForServiceDeployReadiness(
		ctx,
		cfg,
		environment,
		namespace,
		serviceName,
		[]string{serviceName},
		contextData.Service,
		nil,
	); err != nil {
		return fmt.Errorf("stable target not ready after canary promote: %w", err)
	}

	// Cleanup only when canary is no longer referenced by active traffic.
	cleanupStrategyShadowsBestEffort(ctx, cfg, environment, serviceName, "rolling", nil)
	log.Printf("[worker] promote canary: completed for service=%s env=%s", op.Resource, environment)
	return nil
}

func resolveDesiredReplicasForPromote(s models.ServiceConfig) int {
	minReplicas := s.MinReplicas
	if minReplicas < 1 {
		minReplicas = 1
	}
	if s.Replicas > 0 {
		minReplicas = s.Replicas
	}
	if s.MaxReplicas > 0 && minReplicas > s.MaxReplicas {
		return s.MaxReplicas
	}
	return minReplicas
}

func stampDeploymentPromotion(resource map[string]interface{}) {
	spec := shared.MapValue(resource["spec"])
	template := shared.MapValue(spec["template"])
	templateMeta := shared.MapValue(template["metadata"])
	annotations := shared.MapValue(templateMeta["annotations"])
	annotations["releasea.promoted-at"] = time.Now().UTC().Format(time.RFC3339)
	templateMeta["annotations"] = annotations
	template["metadata"] = templateMeta
	spec["template"] = template
	resource["spec"] = spec
}
