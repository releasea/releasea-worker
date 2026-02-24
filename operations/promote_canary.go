package operations

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	strategyworker "releaseaworker/operations/strategy"
)

func handlePromoteCanary(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	if op.Resource == "" {
		return errors.New("service id missing")
	}
	environment := payloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}

	contextData, err := fetchServiceContext(ctx, client, cfg, tokens, op.Resource, environment)
	if err != nil {
		return err
	}
	if resolveDeployStrategyType(contextData.Service) != "canary" {
		return fmt.Errorf("service is not using canary strategy")
	}

	serviceName := toKubeName(contextData.Service.Name)
	if serviceName == "" {
		serviceName = toKubeName(contextData.Service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}
	namespace := resolveNamespace(cfg, environment)
	kubeClient, kubeToken, err := kubeClient()
	if err != nil {
		return err
	}

	canaryName := serviceName + "-canary"
	canaryDeployment, err := fetchResourceAsMap(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, canaryName)
	if err != nil {
		return fmt.Errorf("canary deployment not found (promote only after canary deploy): %w", err)
	}
	canaryService, err := fetchResourceAsMap(ctx, kubeClient, kubeToken, "v1", "Service", namespace, canaryName)
	if err != nil {
		return fmt.Errorf("canary service not found: %w", err)
	}

	desiredReplicas := resolveDesiredReplicasForPromote(contextData.Service)
	stableDeployment := renderTemplateResource(canaryDeployment, map[string]string{})
	stableService := renderTemplateResource(canaryService, map[string]string{})
	if stableDeployment == nil || stableService == nil {
		return errors.New("failed to clone canary resources")
	}
	cleanResourceForReapply(stableDeployment)
	cleanResourceForReapply(stableService)
	strategyworker.RetargetDeployment(stableDeployment, serviceName, serviceName, desiredReplicas)
	stampDeploymentPromotion(stableDeployment)
	strategyworker.RetargetService(stableService, serviceName, serviceName)

	log.Printf("[worker] promote canary: applying stable %s (replicas=%d)", serviceName, desiredReplicas)
	if err := applyResource(ctx, kubeClient, kubeToken, stableDeployment); err != nil {
		return fmt.Errorf("apply stable deployment: %w", err)
	}
	if err := applyResource(ctx, kubeClient, kubeToken, stableService); err != nil {
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

func resolveDesiredReplicasForPromote(s serviceConfig) int {
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
	spec := mapValue(resource["spec"])
	template := mapValue(spec["template"])
	templateMeta := mapValue(template["metadata"])
	annotations := mapValue(templateMeta["annotations"])
	annotations["releasea.promoted-at"] = time.Now().UTC().Format(time.RFC3339)
	templateMeta["annotations"] = annotations
	template["metadata"] = templateMeta
	spec["template"] = template
	resource["spec"] = spec
}
