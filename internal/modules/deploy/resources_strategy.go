package deploy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"releaseaworker/internal/models"
	"releaseaworker/internal/modules/platform"
	"releaseaworker/internal/modules/shared"
	"strings"
	"time"

	strategyworker "releaseaworker/internal/modules/deploy/strategy"
)

func applyServiceWorkloadResources(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	resources []map[string]interface{},
	service models.ServiceConfig,
	logger *platform.DeployLogger,
) error {
	strategyType := ResolveDeployStrategyType(service)
	if logger != nil {
		logger.Logf(ctx, strategyResourceSummary(strategyType, false))
	}

	serviceData := strategyworker.ServiceConfig{
		ID:               service.ID,
		Replicas:         service.Replicas,
		MinReplicas:      service.MinReplicas,
		MaxReplicas:      service.MaxReplicas,
		CPU:              service.CPU,
		DeployTemplateID: service.DeployTemplateID,
		DeploymentStrategy: strategyworker.DeploymentStrategyConfig{
			Type:             service.DeploymentStrategy.Type,
			CanaryPercent:    service.DeploymentStrategy.CanaryPercent,
			BlueGreenPrimary: service.DeploymentStrategy.BlueGreenPrimary,
		},
	}

	deps := strategyworker.Dependencies{
		ApplyResourceFn: func(ctx context.Context, client *http.Client, token string, resource map[string]interface{}) error {
			return platform.ApplyResource(ctx, client, token, resource)
		},
		DeleteResourceFn: func(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) error {
			return platform.DeleteResource(ctx, client, token, apiVersion, kind, namespace, name)
		},
		ResourceExistsFn: func(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (bool, error) {
			return platform.ResourceExists(ctx, client, token, apiVersion, kind, namespace, name)
		},
		FetchResourceFn: func(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (map[string]interface{}, error) {
			return platform.FetchResourceAsMap(ctx, client, token, apiVersion, kind, namespace, name)
		},
		CloneResourceFn: func(resource map[string]interface{}) map[string]interface{} {
			return shared.RenderTemplateResource(resource, map[string]string{})
		},
	}

	err := strategyworker.ApplyServiceWorkloadResources(
		ctx,
		client,
		token,
		namespace,
		serviceName,
		resources,
		serviceData,
		logger,
		deps,
	)
	if err != nil {
		return err
	}
	if logger != nil {
		logger.Logf(ctx, strategyResourceSummary(strategyType, true))
	}
	return nil
}

func reconcileStrategyResources(
	ctx context.Context,
	cfg models.Config,
	ctxData models.DeployContext,
	environment string,
	serviceName string,
	renderedResources []map[string]interface{},
	logger *platform.DeployLogger,
) error {
	strategyType := ResolveDeployStrategyType(ctxData.Service)
	if strategyType == "rolling" {
		// Rolling cleanup must happen only after canonical promotion succeeds.
		return nil
	}

	namespace := shared.ResolveNamespace(cfg, environment)
	client, token, err := platform.KubeClient()
	if err != nil {
		return err
	}

	if strategyType == "canary" {
		stableExists, err := platform.ResourceExists(ctx, client, token, "apps/v1", "Deployment", namespace, serviceName)
		if err != nil {
			return fmt.Errorf("strategy reconcile: %w", err)
		}
		if !stableExists {
			if len(renderedResources) == 0 {
				return fmt.Errorf("strategy reconcile: canary first deploy requires resources")
			}
			return applyServiceWorkloadResources(ctx, client, token, namespace, serviceName, renderedResources, ctxData.Service, logger)
		}
		if len(renderedResources) > 0 {
			// Canary was already applied in applyRenderedResources. Do not overwrite stable.
			return nil
		}
		// Path 1 (raw YAML): stable was updated by kubectl apply but canary was not created.
		// Fetch stable (now with new version), create canary from it, then rollback stable.
		baseDeployment, err := platform.FetchResourceAsMap(ctx, client, token, "apps/v1", "Deployment", namespace, serviceName)
		if err != nil {
			return fmt.Errorf("strategy reconcile: %w", err)
		}
		baseService, err := platform.FetchResourceAsMap(ctx, client, token, "v1", "Service", namespace, serviceName)
		if err != nil {
			return fmt.Errorf("strategy reconcile: %w", err)
		}
		platform.CleanResourceForReapply(baseDeployment)
		platform.CleanResourceForReapply(baseService)

		resources := []map[string]interface{}{baseDeployment, baseService}
		if err := applyServiceWorkloadResources(ctx, client, token, namespace, serviceName, resources, ctxData.Service, logger); err != nil {
			return err
		}

		// Rollback stable to previous version — kubectl apply already updated it, but canary
		// strategy requires stable to keep running the current production version.
		undoArgs := []string{"rollout", "undo", "deployment/" + serviceName, "-n", namespace}
		output, undoErr := platform.RunCommandWithInput(ctx, "kubectl", undoArgs, "")
		if undoErr != nil {
			log.Printf("[worker] canary stable rollback failed: %v", undoErr)
			if logger != nil {
				logger.Logf(ctx, "canary stable rollback failed: %v", undoErr)
			}
		} else if logger != nil {
			logger.Logf(ctx, "stable rollback to previous version: %s", output)
		}

		// Stamp canary deployment to force pod recreation.
		canaryName := serviceName + "-canary"
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"releasea.io/deploy-revision":"%s"}}}}}`, stamp)
		patchArgs := []string{"patch", "deployment", canaryName, "-n", namespace, "-p", patch}
		patchOutput, patchErr := platform.RunCommandWithInput(ctx, "kubectl", patchArgs, "")
		if patchErr != nil {
			log.Printf("[worker] canary stamp failed: %v", patchErr)
		} else if patchOutput != "" && logger != nil {
			logger.Logf(ctx, "canary revision stamp: %s", patchOutput)
		}
		return nil
	}

	if len(renderedResources) > 0 {
		return applyServiceWorkloadResources(
			ctx,
			client,
			token,
			namespace,
			serviceName,
			filterStrategyWorkloadResources(renderedResources, serviceName),
			ctxData.Service,
			logger,
		)
	}
	baseDeployment, err := platform.FetchResourceAsMap(ctx, client, token, "apps/v1", "Deployment", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("strategy reconcile: %w", err)
	}
	baseService, err := platform.FetchResourceAsMap(ctx, client, token, "v1", "Service", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("strategy reconcile: %w", err)
	}

	platform.CleanResourceForReapply(baseDeployment)
	platform.CleanResourceForReapply(baseService)

	resources := []map[string]interface{}{baseDeployment, baseService}
	return applyServiceWorkloadResources(ctx, client, token, namespace, serviceName, resources, ctxData.Service, logger)
}

func filterStrategyWorkloadResources(resources []map[string]interface{}, serviceName string) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, 2)
	serviceName = strings.TrimSpace(serviceName)
	var deploymentFallback map[string]interface{}
	var serviceFallback map[string]interface{}

	for _, resource := range resources {
		if resource == nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(shared.StringValue(resource, "kind")))
		if kind != "deployment" && kind != "service" {
			continue
		}
		meta := shared.MapValue(resource["metadata"])
		name := strings.TrimSpace(shared.StringValue(meta, "name"))
		switch kind {
		case "deployment":
			if deploymentFallback == nil {
				deploymentFallback = resource
			}
			if serviceName != "" && name == serviceName {
				deploymentFallback = resource
			}
		case "service":
			if serviceFallback == nil {
				serviceFallback = resource
			}
			if serviceName != "" && name == serviceName {
				serviceFallback = resource
			}
		}
	}
	if deploymentFallback != nil {
		filtered = append(filtered, deploymentFallback)
	}
	if serviceFallback != nil {
		filtered = append(filtered, serviceFallback)
	}
	return filtered
}

func strategyResourceSummary(strategyType string, completed bool) string {
	if completed {
		switch strategyType {
		case "canary":
			return "Canary resources applied"
		case "blue-green":
			return "Blue/green resources applied"
		default:
			return "Rolling resources applied"
		}
	}
	switch strategyType {
	case "canary":
		return "Applying canary resources and traffic split"
	case "blue-green":
		return "Applying blue/green slot resources"
	default:
		return "Applying rolling resources"
	}
}
