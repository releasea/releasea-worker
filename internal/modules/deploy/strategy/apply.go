package strategy

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	commonvalues "releaseaworker/internal/modules/shared/values"
)

func ApplyServiceWorkloadResources(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	resources []map[string]interface{},
	service ServiceConfig,
	logger Logger,
	deps Dependencies,
) error {
	if err := deps.validate(); err != nil {
		return err
	}

	strategyType := normalizeStrategyType(service)
	switch strategyType {
	case "canary":
		return applyCanaryWorkloadResources(ctx, client, token, namespace, serviceName, resources, service, logger, deps)
	case "blue-green":
		return applyBlueGreenWorkloadResources(ctx, client, token, namespace, serviceName, resources, service, logger, deps)
	default:
		return applyRollingWorkloadResources(ctx, client, token, namespace, serviceName, resources, service, logger, deps)
	}
}

func applyRollingWorkloadResources(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	resources []map[string]interface{},
	service ServiceConfig,
	logger Logger,
	deps Dependencies,
) error {
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(commonvalues.StringValue(resource, "kind")))
		name := strings.TrimSpace(resourceName(resource))
		if kind == "deployment" {
			applyDeploymentPolicy(resource, service)
		}
		if kind == "service" && name == serviceName && deps.FetchResourceFn != nil {
			existingService, fetchErr := deps.FetchResourceFn(ctx, client, token, "v1", "Service", namespace, serviceName)
			if fetchErr == nil && len(existingService) > 0 {
				existingSpec := commonvalues.MapValue(existingService["spec"])
				existingSelector := commonvalues.MapValue(existingSpec["selector"])
				currentTarget := strings.TrimSpace(commonvalues.StringValue(existingSelector, "app"))
				if currentTarget != "" && currentTarget != serviceName {
					spec := commonvalues.MapValue(resource["spec"])
					selector := commonvalues.MapValue(spec["selector"])
					selector["app"] = currentTarget
					spec["selector"] = selector
					resource["spec"] = spec
					if logger != nil {
						logger.Logf(ctx, "rolling staging keeps canonical service on %s until validation completes", currentTarget)
					}
				}
			}
		}
		if logger != nil {
			kindName := commonvalues.StringValue(resource, "kind")
			meta := commonvalues.MapValue(resource["metadata"])
			name := commonvalues.StringValue(meta, "name")
			if kindName != "" && name != "" {
				logger.Logf(ctx, "applying %s %s", kindName, name)
			}
		}
		if err := deps.ApplyResourceFn(ctx, client, token, resource); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}
	return syncAutoscalerResource(ctx, client, token, namespace, serviceName, service, logger, deps)
}

func applyCanaryWorkloadResources(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	resources []map[string]interface{},
	service ServiceConfig,
	logger Logger,
	deps Dependencies,
) error {
	baseDeployment, baseService, others, err := splitWorkloadResources(resources, serviceName)
	if err != nil {
		return err
	}
	for _, resource := range others {
		if err := deps.ApplyResourceFn(ctx, client, token, resource); err != nil {
			return err
		}
	}

	stableExists, err := deps.ResourceExistsFn(ctx, client, token, "apps/v1", "Deployment", namespace, serviceName)
	if err != nil {
		return err
	}

	if !stableExists {
		canonicalTarget := resolveCanonicalServiceSelector(ctx, client, token, namespace, serviceName, deps)
		preserveCurrentStable := canonicalTarget != "" && canonicalTarget != serviceName
		if !preserveCurrentStable {
			// First canary deploy: no previous version. Apply both stable and canary with the new version.
			stableService := cloneResource(baseService, deps)
			RetargetService(stableService, serviceName, serviceName)
			if err := deps.ApplyResourceFn(ctx, client, token, stableService); err != nil {
				return err
			}
			stableDeployment := cloneResource(baseDeployment, deps)
			RetargetDeployment(stableDeployment, serviceName, serviceName, resolveDesiredReplicas(service))
			setDeploymentRollingStrategy(stableDeployment)
			if err := deps.ApplyResourceFn(ctx, client, token, stableDeployment); err != nil {
				return err
			}
			if logger != nil {
				logger.Logf(ctx, "canary first deploy: stable %s created with new version", serviceName)
				logger.Flush(ctx)
			}
		} else if logger != nil {
			logger.Logf(ctx, "canary transition: preserving stable traffic on %s while deploying canary", canonicalTarget)
			logger.Flush(ctx)
		}
	}
	// When stableExists, leave stable (current production) untouched; only deploy new version to canary.

	canaryName := serviceName + "-canary"
	canaryReplicas := resolveCanaryReplicas(service)
	canaryDeployment := cloneResource(baseDeployment, deps)
	RetargetDeployment(canaryDeployment, canaryName, canaryName, canaryReplicas)
	setDeploymentRollingStrategy(canaryDeployment)
	if err := deps.ApplyResourceFn(ctx, client, token, canaryDeployment); err != nil {
		return err
	}
	canaryService := cloneResource(baseService, deps)
	RetargetService(canaryService, canaryName, canaryName)
	if err := deps.ApplyResourceFn(ctx, client, token, canaryService); err != nil {
		return err
	}

	if err := deps.DeleteResourceFn(ctx, client, token, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName); err != nil {
		return err
	}
	if logger != nil {
		if stableExists {
			logger.Logf(ctx, "canary rollout active (stable=previous version canary=new version weight=%d%%)", resolveCanaryPercent(service))
		} else {
			logger.Logf(ctx, "canary rollout active (stable=%s canary=%s weight=%d%%)", serviceName, canaryName, resolveCanaryPercent(service))
		}
		logger.Flush(ctx)
	}
	return nil
}

func resolveCanonicalServiceSelector(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	deps Dependencies,
) string {
	if deps.FetchResourceFn == nil {
		return ""
	}
	canonicalService, err := deps.FetchResourceFn(ctx, client, token, "v1", "Service", namespace, serviceName)
	if err != nil || len(canonicalService) == 0 {
		return ""
	}
	spec := commonvalues.MapValue(canonicalService["spec"])
	selector := commonvalues.MapValue(spec["selector"])
	return strings.TrimSpace(commonvalues.StringValue(selector, "app"))
}

func applyBlueGreenWorkloadResources(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	resources []map[string]interface{},
	service ServiceConfig,
	logger Logger,
	deps Dependencies,
) error {
	baseDeployment, baseService, others, err := splitWorkloadResources(resources, serviceName)
	if err != nil {
		return err
	}
	for _, resource := range others {
		if err := deps.ApplyResourceFn(ctx, client, token, resource); err != nil {
			return err
		}
	}

	activeColor := resolvePrimaryBlueGreenColor(service)
	candidateColor := oppositeBlueGreenColor(activeColor)
	activeName := serviceName + "-" + activeColor
	candidateName := serviceName + "-" + candidateColor

	activeExists, err := deps.ResourceExistsFn(ctx, client, token, "apps/v1", "Deployment", namespace, activeName)
	if err != nil {
		return err
	}
	legacyStableExists, err := deps.ResourceExistsFn(ctx, client, token, "apps/v1", "Deployment", namespace, serviceName)
	if err != nil {
		return err
	}

	if !activeExists {
		activeDeploymentSource := baseDeployment
		activeServiceSource := baseService
		if deps.FetchResourceFn != nil {
			if fetchedDeployment, fetchErr := deps.FetchResourceFn(ctx, client, token, "apps/v1", "Deployment", namespace, serviceName); fetchErr == nil && len(fetchedDeployment) > 0 {
				activeDeploymentSource = fetchedDeployment
			}
			if fetchedService, fetchErr := deps.FetchResourceFn(ctx, client, token, "v1", "Service", namespace, serviceName); fetchErr == nil && len(fetchedService) > 0 {
				activeServiceSource = fetchedService
			}
		}

		activeDeployment := cloneResource(activeDeploymentSource, deps)
		sanitizeResourceForApply(activeDeployment)
		RetargetDeployment(activeDeployment, activeName, activeName, resolveDesiredReplicas(service))
		setDeploymentRollingStrategy(activeDeployment)
		if err := deps.ApplyResourceFn(ctx, client, token, activeDeployment); err != nil {
			return err
		}

		activeService := cloneResource(activeServiceSource, deps)
		sanitizeResourceForApply(activeService)
		RetargetService(activeService, activeName, activeName)
		if err := deps.ApplyResourceFn(ctx, client, token, activeService); err != nil {
			return err
		}
	}

	candidateDeployment := cloneResource(baseDeployment, deps)
	sanitizeResourceForApply(candidateDeployment)
	RetargetDeployment(candidateDeployment, candidateName, candidateName, resolveDesiredReplicas(service))
	setDeploymentRollingStrategy(candidateDeployment)
	if err := deps.ApplyResourceFn(ctx, client, token, candidateDeployment); err != nil {
		return err
	}

	candidateService := cloneResource(baseService, deps)
	sanitizeResourceForApply(candidateService)
	RetargetService(candidateService, candidateName, candidateName)
	if err := deps.ApplyResourceFn(ctx, client, token, candidateService); err != nil {
		return err
	}

	canonicalService := cloneResource(baseService, deps)
	sanitizeResourceForApply(canonicalService)
	canonicalTarget := activeName
	if !activeExists && legacyStableExists {
		// Preserve current traffic target while the blue/green pools are being staged.
		canonicalTarget = serviceName
	}
	RetargetService(canonicalService, serviceName, canonicalTarget)
	if err := deps.ApplyResourceFn(ctx, client, token, canonicalService); err != nil {
		return err
	}

	if logger != nil {
		logger.Logf(ctx, "blue-green staged (active=%s candidate=%s)", activeName, candidateName)
		logger.Flush(ctx)
	}
	return nil
}

func splitWorkloadResources(resources []map[string]interface{}, serviceName string) (map[string]interface{}, map[string]interface{}, []map[string]interface{}, error) {
	var deployment map[string]interface{}
	var service map[string]interface{}
	others := make([]map[string]interface{}, 0, len(resources))
	var fallbackDeployment map[string]interface{}
	var fallbackService map[string]interface{}

	for _, resource := range resources {
		if resource == nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(commonvalues.StringValue(resource, "kind")))
		name := strings.TrimSpace(resourceName(resource))
		switch kind {
		case "deployment":
			if name == serviceName && deployment == nil {
				deployment = resource
				continue
			}
			if fallbackDeployment == nil {
				fallbackDeployment = resource
				continue
			}
		case "service":
			if name == serviceName && service == nil {
				service = resource
				continue
			}
			if fallbackService == nil {
				fallbackService = resource
				continue
			}
		}
		others = append(others, resource)
	}
	if deployment == nil {
		deployment = fallbackDeployment
	}
	if service == nil {
		service = fallbackService
	}
	if deployment == nil || service == nil {
		return nil, nil, nil, fmt.Errorf("strategy deploy requires deployment and service resources")
	}
	return deployment, service, others, nil
}
