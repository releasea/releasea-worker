package strategy

import (
	"context"
	"net/http"
	"strings"

	"releaseaworker/internal/modules/shared/deploystrategy"
	commonvalues "releaseaworker/internal/modules/shared/values"
)

func normalizeStrategyType(service ServiceConfig) string {
	return deploystrategy.NormalizeType(service.DeployTemplateID, service.DeploymentStrategy.Type)
}

func resolveCanaryPercent(service ServiceConfig) int {
	return deploystrategy.NormalizeCanaryPercent(service.DeploymentStrategy.CanaryPercent)
}

func resolveCanaryReplicas(service ServiceConfig) int {
	base := resolveDesiredReplicas(service)
	percent := resolveCanaryPercent(service)
	replicas := (base*percent + 99) / 100
	if replicas < 1 {
		replicas = 1
	}
	return replicas
}

func resolvePrimaryBlueGreenColor(service ServiceConfig) string {
	primary, _ := deploystrategy.ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
	return primary
}

func oppositeBlueGreenColor(primary string) string {
	_, secondary := deploystrategy.ResolveBlueGreenSlots(primary)
	return secondary
}

func applyDeploymentPolicy(resource map[string]interface{}, service ServiceConfig) {
	kind := strings.ToLower(strings.TrimSpace(commonvalues.StringValue(resource, "kind")))
	if kind != "deployment" {
		return
	}
	spec := commonvalues.MapValue(resource["spec"])
	if spec == nil {
		return
	}
	spec["replicas"] = resolveDesiredReplicas(service)
	setDeploymentRollingStrategy(resource)
}

func setDeploymentRollingStrategy(resource map[string]interface{}) {
	spec := commonvalues.MapValue(resource["spec"])
	spec["strategy"] = map[string]interface{}{
		"type": "RollingUpdate",
		"rollingUpdate": map[string]interface{}{
			"maxSurge":       "25%",
			"maxUnavailable": "25%",
		},
	}
	resource["spec"] = spec
}

func resolveDesiredReplicas(service ServiceConfig) int {
	minReplicas := service.MinReplicas
	if minReplicas < 1 {
		minReplicas = 1
	}
	if service.Replicas > 0 {
		minReplicas = service.Replicas
	}
	if service.MaxReplicas > 0 && minReplicas > service.MaxReplicas {
		return service.MaxReplicas
	}
	return minReplicas
}

func syncAutoscalerResource(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	service ServiceConfig,
	logger Logger,
	deps Dependencies,
) error {
	if strings.EqualFold(service.DeployTemplateID, "tpl-cronjob") {
		return deps.DeleteResourceFn(ctx, client, token, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName)
	}
	if normalizeStrategyType(service) == "canary" || normalizeStrategyType(service) == "blue-green" {
		return deps.DeleteResourceFn(ctx, client, token, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName)
	}
	minReplicas := service.MinReplicas
	if minReplicas < 1 {
		minReplicas = resolveDesiredReplicas(service)
	}
	maxReplicas := service.MaxReplicas
	if maxReplicas < 1 || maxReplicas <= minReplicas {
		if logger != nil {
			logger.Logf(ctx, "autoscaling disabled (min=%d max=%d)", minReplicas, maxReplicas)
			logger.Flush(ctx)
		}
		return deps.DeleteResourceFn(ctx, client, token, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName)
	}
	targetCPU := service.CPU
	if targetCPU <= 0 {
		targetCPU = 70
	}
	hpa := map[string]interface{}{
		"apiVersion": "autoscaling/v2",
		"kind":       "HorizontalPodAutoscaler",
		"metadata": map[string]interface{}{
			"name":      serviceName,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app":              serviceName,
				"releasea.service": service.ID,
			},
		},
		"spec": map[string]interface{}{
			"scaleTargetRef": map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"name":       serviceName,
			},
			"minReplicas": minReplicas,
			"maxReplicas": maxReplicas,
			"metrics": []interface{}{
				map[string]interface{}{
					"type": "Resource",
					"resource": map[string]interface{}{
						"name": "cpu",
						"target": map[string]interface{}{
							"type":               "Utilization",
							"averageUtilization": targetCPU,
						},
					},
				},
			},
		},
	}
	if logger != nil {
		logger.Logf(ctx, "applying HorizontalPodAutoscaler %s (min=%d max=%d cpu=%d%%)", serviceName, minReplicas, maxReplicas, targetCPU)
	}
	if err := deps.ApplyResourceFn(ctx, client, token, hpa); err != nil {
		return err
	}
	if logger != nil {
		logger.Flush(ctx)
	}
	return nil
}
