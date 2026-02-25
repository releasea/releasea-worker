package ops

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func isWorkloadReadinessRequired(serviceType, deployTemplateID string) bool {
	if strings.EqualFold(strings.TrimSpace(serviceType), "static-site") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(deployTemplateID), "tpl-cronjob") {
		return false
	}
	return true
}

func resolveStrategyDeploymentTargets(service serviceConfig, serviceName string) []string {
	return resolveWorkloadTargets(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName)
}

func resolveServicePayloadDeploymentTargets(service servicePayload, serviceName string) []string {
	return resolveWorkloadTargets(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName)
}

func resolveDeployReadinessTargets(service serviceConfig, serviceName string, payload map[string]interface{}) []string {
	hints := extractDeploymentResourceHints(payload)
	return resolveWorkloadTargetsWithHints(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName, hints)
}

func resolveServicePayloadDeployReadinessTargets(service servicePayload, serviceName string, payload map[string]interface{}) []string {
	hints := extractDeploymentResourceHints(payload)
	return resolveWorkloadTargetsWithHints(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName, hints)
}

func resolveDeployNamespaceFromPayload(payload map[string]interface{}, fallback string) string {
	for _, hint := range extractDeploymentResourceHints(payload) {
		namespace := strings.TrimSpace(hint.namespace)
		if namespace != "" {
			return namespace
		}
	}
	return strings.TrimSpace(fallback)
}

func resolveWorkloadTargets(serviceType, deployTemplateID string, strategy deploymentStrategyConfig, serviceName string) []string {
	return resolveWorkloadTargetsWithHints(serviceType, deployTemplateID, strategy, serviceName, nil)
}

func resolveWorkloadTargetsWithHints(
	serviceType,
	deployTemplateID string,
	strategy deploymentStrategyConfig,
	serviceName string,
	hints []deploymentResourceHint,
) []string {
	normalizedService := strings.TrimSpace(serviceName)
	hintedDeployments := collectHintedDeploymentNames(hints)
	if normalizedService == "" && len(hintedDeployments) > 0 {
		normalizedService = hintedDeployments[0]
	}
	if normalizedService == "" && len(hintedDeployments) == 0 {
		return nil
	}
	if !isWorkloadReadinessRequired(serviceType, deployTemplateID) {
		return nil
	}

	serviceData := serviceConfig{
		Type:             serviceType,
		DeployTemplateID: deployTemplateID,
		DeploymentStrategy: deploymentStrategyConfig{
			Type:             strategy.Type,
			CanaryPercent:    strategy.CanaryPercent,
			BlueGreenPrimary: strategy.BlueGreenPrimary,
		},
	}

	strategyType := resolveDeployStrategyType(serviceData)
	targets := []string{}
	switch strategyType {
	case "canary":
		targets = append(targets, normalizedService, normalizedService+"-canary")
	case "blue-green":
		primary, secondary := resolveBlueGreenSlots(strategy.BlueGreenPrimary)
		targets = append(targets, normalizedService+"-"+primary, normalizedService+"-"+secondary)
	default:
		if len(hintedDeployments) > 0 {
			targets = append(targets, hintedDeployments...)
		} else {
			targets = append(targets, normalizedService)
		}
	}
	return uniqueStrings(targets)
}

func waitForServiceDeployReadiness(
	ctx context.Context,
	cfg Config,
	environment string,
	namespace string,
	serviceName string,
	targets []string,
	service serviceConfig,
	logger *deployLogger,
) error {
	if len(targets) == 0 {
		targets = resolveStrategyDeploymentTargets(service, serviceName)
		if len(targets) == 0 {
			return nil
		}
	}

	timeoutSeconds := envInt("WORKER_DEPLOY_READY_TIMEOUT_SECONDS", 420)
	if timeoutSeconds < 30 {
		timeoutSeconds = 30
	}
	pollSeconds := envInt("WORKER_DEPLOY_READY_POLL_SECONDS", 5)
	if pollSeconds < 1 {
		pollSeconds = 1
	}

	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = resolveNamespace(cfg, environment)
	}
	kubeClient, kubeToken, err := kubeClient()
	if err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	lastPending := ""

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pending := make([]string, 0)
		for _, target := range targets {
			deploy, err := fetchDeployment(ctx, kubeClient, kubeToken, namespace, target)
			if err != nil {
				if err == errDeploymentNotFound {
					pending = append(pending, fmt.Sprintf("%s not found", target))
					continue
				}
				return fmt.Errorf("failed to inspect deployment %s: %w", target, err)
			}
			if failed, reason := deploymentFailureReason(deploy); failed {
				return fmt.Errorf("%s: %s", target, reason)
			}
			if podFailed, podReason := evaluateDeploymentPods(ctx, kubeClient, kubeToken, namespace, target); podFailed {
				return fmt.Errorf("%s", podReason)
			}
			if deploy.Status.AvailableReplicas < 1 {
				pending = append(
					pending,
					fmt.Sprintf("%s unavailable (available=%d desired=%d)", target, deploy.Status.AvailableReplicas, deploy.Status.Replicas),
				)
			}
		}

		if len(pending) == 0 {
			if logger != nil {
				logger.Logf(ctx, "workloads ready: %s", strings.Join(targets, ", "))
				logger.Flush(ctx)
			}
			return nil
		}

		pendingSummary := strings.Join(pending, "; ")
		if logger != nil && pendingSummary != lastPending {
			logger.Logf(ctx, "waiting for workloads: %s", pendingSummary)
			logger.Flush(ctx)
			lastPending = pendingSummary
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for workload readiness: %s", pendingSummary)
		}

		timer := time.NewTimer(time.Duration(pollSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func deploymentFailureReason(deploy deploymentInfo) (bool, string) {
	for _, condition := range deploy.Status.Conditions {
		if condition.Type == "Progressing" && strings.EqualFold(condition.Status, "False") {
			if reason := strings.TrimSpace(condition.Reason); reason != "" {
				return true, reason
			}
			if message := strings.TrimSpace(condition.Message); message != "" {
				return true, message
			}
			return true, "deployment not progressing"
		}
		if condition.Type == "Available" && strings.EqualFold(condition.Status, "False") {
			if strings.EqualFold(strings.TrimSpace(condition.Reason), "ProgressDeadlineExceeded") {
				return true, "progress deadline exceeded"
			}
		}
		if condition.Type == "ReplicaFailure" && strings.EqualFold(condition.Status, "True") {
			if reason := strings.TrimSpace(condition.Reason); reason != "" {
				return true, reason
			}
			if message := strings.TrimSpace(condition.Message); message != "" {
				return true, message
			}
			return true, "replica failure"
		}
	}
	return false, ""
}

type deploymentResourceHint struct {
	name      string
	namespace string
}

func extractDeploymentResourceHints(payload map[string]interface{}) []deploymentResourceHint {
	resources, err := payloadResources(payload)
	if err != nil || len(resources) == 0 {
		return nil
	}
	hints := make([]deploymentResourceHint, 0)
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(stringValue(resource, "kind")))
		if kind != "deployment" {
			continue
		}
		meta := mapValue(resource["metadata"])
		hint := deploymentResourceHint{
			name:      strings.TrimSpace(stringValue(meta, "name")),
			namespace: strings.TrimSpace(stringValue(meta, "namespace")),
		}
		if hint.name == "" && hint.namespace == "" {
			continue
		}
		hints = append(hints, hint)
	}
	return hints
}

func collectHintedDeploymentNames(hints []deploymentResourceHint) []string {
	out := make([]string, 0, len(hints))
	for _, hint := range hints {
		out = append(out, hint.name)
	}
	return uniqueStrings(out)
}
