package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/shared"
	"strings"
	"time"
)

var errDeploymentNotFound = platform.ErrDeploymentNotFound

func IsWorkloadReadinessRequired(serviceType, deployTemplateID string) bool {
	if strings.EqualFold(strings.TrimSpace(serviceType), "static-site") {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(deployTemplateID), "tpl-cronjob") {
		return false
	}
	return true
}

func resolveStrategyDeploymentTargets(service models.ServiceConfig, serviceName string) []string {
	return resolveWorkloadTargets(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName)
}

func ResolveServicePayloadDeploymentTargets(service models.ServicePayload, serviceName string) []string {
	return resolveWorkloadTargets(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName)
}

func resolveDeployReadinessTargets(service models.ServiceConfig, serviceName string, payload map[string]interface{}) []string {
	hints := extractDeploymentResourceHints(payload)
	return resolveWorkloadTargetsWithHints(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName, hints)
}

func ResolveServicePayloadDeployReadinessTargets(service models.ServicePayload, serviceName string, payload map[string]interface{}) []string {
	hints := extractDeploymentResourceHints(payload)
	return resolveWorkloadTargetsWithHints(service.Type, service.DeployTemplateID, service.DeploymentStrategy, serviceName, hints)
}

func ResolveDeployNamespaceFromPayload(payload map[string]interface{}, fallback string) string {
	for _, hint := range extractDeploymentResourceHints(payload) {
		namespace := strings.TrimSpace(hint.namespace)
		if namespace != "" {
			return namespace
		}
	}
	return strings.TrimSpace(fallback)
}

func resolveWorkloadTargets(serviceType, deployTemplateID string, strategy models.DeploymentStrategyConfig, serviceName string) []string {
	return resolveWorkloadTargetsWithHints(serviceType, deployTemplateID, strategy, serviceName, nil)
}

func resolveWorkloadTargetsWithHints(
	serviceType,
	deployTemplateID string,
	strategy models.DeploymentStrategyConfig,
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
	if !IsWorkloadReadinessRequired(serviceType, deployTemplateID) {
		return nil
	}

	serviceData := models.ServiceConfig{
		Type:             serviceType,
		DeployTemplateID: deployTemplateID,
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type:             strategy.Type,
			CanaryPercent:    strategy.CanaryPercent,
			BlueGreenPrimary: strategy.BlueGreenPrimary,
		},
	}

	strategyType := ResolveDeployStrategyType(serviceData)
	targets := []string{}
	switch strategyType {
	case "canary":
		targets = append(targets, normalizedService, normalizedService+"-canary")
	case "blue-green":
		primary, secondary := ResolveBlueGreenSlots(strategy.BlueGreenPrimary)
		targets = append(targets, normalizedService+"-"+primary, normalizedService+"-"+secondary)
	default:
		if len(hintedDeployments) > 0 {
			targets = append(targets, hintedDeployments...)
		} else {
			targets = append(targets, normalizedService)
		}
	}
	return shared.UniqueStrings(targets)
}

func waitForServiceDeployReadiness(
	ctx context.Context,
	cfg models.Config,
	environment string,
	namespace string,
	serviceName string,
	targets []string,
	service models.ServiceConfig,
	logger *platform.DeployLogger,
) error {
	if len(targets) == 0 {
		targets = resolveStrategyDeploymentTargets(service, serviceName)
		if len(targets) == 0 {
			return nil
		}
	}

	timeoutSeconds := shared.EnvInt("WORKER_DEPLOY_READY_TIMEOUT_SECONDS", 420)
	if timeoutSeconds < 30 {
		timeoutSeconds = 30
	}
	pollSeconds := shared.EnvInt("WORKER_DEPLOY_READY_POLL_SECONDS", 5)
	if pollSeconds < 1 {
		pollSeconds = 1
	}

	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		namespace = shared.ResolveNamespace(cfg, environment)
	}
	kubeClient, kubeToken, err := platform.KubeClient()
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

func deploymentFailureReason(deploy models.DeploymentInfo) (bool, string) {
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
	resources, err := platform.PayloadResources(payload)
	if err != nil || len(resources) == 0 {
		return nil
	}
	hints := make([]deploymentResourceHint, 0)
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(shared.StringValue(resource, "kind")))
		if kind != "deployment" {
			continue
		}
		meta := shared.MapValue(resource["metadata"])
		hint := deploymentResourceHint{
			name:      strings.TrimSpace(shared.StringValue(meta, "name")),
			namespace: strings.TrimSpace(shared.StringValue(meta, "namespace")),
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
	return shared.UniqueStrings(out)
}

func fetchDeployment(ctx context.Context, client *http.Client, token, namespace, name string) (models.DeploymentInfo, error) {
	var deployment models.DeploymentInfo
	url := fmt.Sprintf("https://kubernetes.default.svc/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return deployment, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return deployment, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return deployment, errDeploymentNotFound
	}
	if resp.StatusCode >= 400 {
		return deployment, fmt.Errorf("kubernetes api error: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&deployment); err != nil {
		return deployment, err
	}
	return deployment, nil
}

func evaluateDeploymentPods(ctx context.Context, client *http.Client, token, namespace, serviceName string) (bool, string) {
	if serviceName == "" {
		return false, ""
	}
	pods, err := fetchPodsByLabel(ctx, client, token, namespace, "app="+serviceName)
	if err != nil {
		log.Printf("[worker] curator pod fetch error: %v", err)
		return false, ""
	}
	if len(pods.Items) == 0 {
		return false, ""
	}
	for _, pod := range pods.Items {
		if failed, reason := evaluatePodFailure(pod); failed {
			return true, reason
		}
	}
	return false, ""
}

func fetchPodsByLabel(ctx context.Context, client *http.Client, token, namespace, selector string) (models.PodList, error) {
	var pods models.PodList
	encoded := url.QueryEscape(selector)
	url := fmt.Sprintf("https://kubernetes.default.svc/api/v1/namespaces/%s/pods?labelSelector=%s", namespace, encoded)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return pods, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return pods, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return pods, fmt.Errorf("kubernetes api error: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&pods); err != nil {
		return pods, err
	}
	return pods, nil
}

func evaluatePodFailure(pod models.PodInfo) (bool, string) {
	podName := strings.TrimSpace(pod.Metadata.Name)
	if podName == "" {
		podName = "pod"
	}
	if strings.EqualFold(pod.Status.Phase, "Failed") {
		return true, fmt.Sprintf("%s failed", podName)
	}

	containers := append([]models.ContainerStatus{}, pod.Status.InitContainerStatuses...)
	containers = append(containers, pod.Status.ContainerStatuses...)

	for _, status := range containers {
		if status.State.Waiting != nil {
			if failed, reason := waitingFailureReason(status.State.Waiting); failed {
				return true, fmt.Sprintf("%s: %s", podName, reason)
			}
		}
		if status.State.Terminated != nil {
			if failed, reason := terminatedFailureReason(status.State.Terminated); failed {
				return true, fmt.Sprintf("%s: %s", podName, reason)
			}
		}
		if status.LastState.Terminated != nil {
			if failed, reason := terminatedFailureReason(status.LastState.Terminated); failed {
				if status.RestartCount >= 1 {
					return true, fmt.Sprintf("%s: %s", podName, reason)
				}
			}
		}
		if !status.Ready && !strings.EqualFold(status.Name, "istio-proxy") && status.RestartCount >= 3 {
			return true, fmt.Sprintf("%s: %s restarting", podName, status.Name)
		}
	}
	return false, ""
}

func waitingFailureReason(waiting *models.ContainerStateWaiting) (bool, string) {
	if waiting == nil {
		return false, ""
	}
	reason := strings.TrimSpace(waiting.Reason)
	if reason == "" {
		return false, ""
	}
	switch reason {
	case "CrashLoopBackOff":
		return true, "CrashLoopBackOff"
	case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "RegistryUnavailable":
		return true, reason
	case "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
		return true, reason
	default:
		return false, ""
	}
}

func terminatedFailureReason(term *models.ContainerStateTerminated) (bool, string) {
	if term == nil {
		return false, ""
	}
	if term.ExitCode == 0 {
		return false, ""
	}
	reason := strings.TrimSpace(term.Reason)
	if reason == "" {
		reason = fmt.Sprintf("exit code %d", term.ExitCode)
	}
	return true, reason
}
