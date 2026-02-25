package maintenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"releaseaworker/internal/models"
	deploymodule "releaseaworker/internal/modules/deploy"
	"releaseaworker/internal/modules/platform"
	rulesmodule "releaseaworker/internal/modules/rules"
	"releaseaworker/internal/modules/shared"
	"strings"
	"time"
)

var errDeploymentNotFound = platform.ErrDeploymentNotFound

func RunCurator(ctx context.Context, cfg models.Config, tokens *platform.TokenManager) {
	interval := time.Duration(shared.EnvInt("WORKER_CURATOR_SECONDS", 60)) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	maxAge := time.Duration(shared.EnvInt("WORKER_CURATOR_MAX_SECONDS", 600)) * time.Second
	if maxAge <= 0 {
		maxAge = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	client := &http.Client{Timeout: 15 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := curateDeployments(ctx, client, cfg, tokens, maxAge); err != nil {
				log.Printf("[worker] curator error: %v", err)
			}
			if err := curateRuleDeploys(ctx, client, cfg, tokens, maxAge); err != nil {
				log.Printf("[worker] curator error: %v", err)
			}
		}
	}
}

func curateDeployments(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, maxAge time.Duration) error {
	ops, err := platform.FetchOperationsByStatus(ctx, client, cfg, tokens, "in-progress", "service.deploy")
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}

	kubeClient, kubeToken, err := platform.KubeClient()
	if err != nil {
		return err
	}

	for _, op := range ops {
		env := platform.PayloadString(op.Payload, "environment")
		if env == "" {
			env = "prod"
		}
		var service models.ServicePayload
		serviceFetched := false
		if op.Resource != "" {
			fetched, err := rulesmodule.FetchService(ctx, client, cfg, tokens, op.Resource)
			if err == nil {
				service = fetched
				serviceFetched = true
				if !deploymodule.IsWorkloadReadinessRequired(service.Type, service.DeployTemplateID) {
					continue
				}
			}
		}
		name := shared.ToKubeName(op.ServiceName)
		if name == "" && serviceFetched {
			name = shared.ToKubeName(service.Name)
		}
		if name == "" {
			name = shared.ToKubeName(op.Resource)
		}
		if name == "" {
			continue
		}
		namespace := deploymodule.ResolveDeployNamespaceFromPayload(op.Payload, shared.ResolveNamespace(cfg, env))

		targets := []string{name}
		if serviceFetched {
			resolvedTargets := deploymodule.ResolveServicePayloadDeployReadinessTargets(service, name, op.Payload)
			if len(resolvedTargets) > 0 {
				targets = resolvedTargets
			}
		}

		allReady := true
		failed := false
		for _, target := range targets {
			deploy, statusErr := fetchDeployment(ctx, kubeClient, kubeToken, namespace, target)
			if statusErr != nil {
				if errors.Is(statusErr, errDeploymentNotFound) {
					allReady = false
					if opAgeExceeded(op, maxAge) {
						_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", fmt.Sprintf("deployment %s not found", target))
						failed = true
						break
					}
					continue
				}
				log.Printf("[worker] curator deployment fetch error (%s): %v", target, statusErr)
				allReady = false
				continue
			}

			ready, deployFailed, reason := evaluateDeploymentStatus(deploy, op, maxAge)
			if deployFailed {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", fmt.Sprintf("%s: %s", target, reason))
				failed = true
				break
			}
			if !ready {
				allReady = false
			}

			podFailed, podReason := evaluateDeploymentPods(ctx, kubeClient, kubeToken, namespace, target)
			if podFailed {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", podReason)
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		if allReady {
			_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "succeeded", "")
		}
	}
	return nil
}

func curateRuleDeploys(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, maxAge time.Duration) error {
	ops, err := platform.FetchOperationsByStatus(ctx, client, cfg, tokens, "in-progress", "rule.deploy")
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}

	kubeClient, kubeToken, err := platform.KubeClient()
	if err != nil {
		return err
	}

	for _, op := range ops {
		if op.Resource == "" {
			continue
		}
		rule, err := rulesmodule.FetchRule(ctx, client, cfg, tokens, op.Resource)
		if err != nil {
			if opAgeExceeded(op, maxAge) {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", "rule fetch failed")
			}
			continue
		}
		service, err := rulesmodule.FetchService(ctx, client, cfg, tokens, rule.ServiceID)
		if err != nil {
			if opAgeExceeded(op, maxAge) {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", "service fetch failed")
			}
			continue
		}

		env := strings.TrimSpace(rule.Environment)
		if env == "" {
			env = "prod"
		}
		serviceName := shared.ToKubeName(service.Name)
		if serviceName == "" {
			serviceName = shared.ToKubeName(service.ID)
		}
		if serviceName == "" {
			continue
		}

		namespace := shared.ResolveNamespace(cfg, env)
		if err := shared.ValidateAppNamespace(namespace); err != nil {
			continue
		}

		if rulesmodule.RuleAction(rule) == "deny" {
			policyName := rulesmodule.BuildDenyPolicyName(serviceName, rule.Name, rule.ID)
			exists, err := platform.ResourceExists(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, policyName)
			if err != nil {
				if opAgeExceeded(op, maxAge) {
					_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", "authorization policy check failed")
				}
				continue
			}
			if exists {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "succeeded", "")
				continue
			}
			if opAgeExceeded(op, maxAge) {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", "authorization policy missing")
			}
			continue
		}

		vsName := rulesmodule.RuleVirtualServiceName(serviceName, rule.ID)
		exists, err := platform.ResourceExists(ctx, kubeClient, kubeToken, "networking.istio.io/v1beta1", "VirtualService", namespace, vsName)
		if err != nil {
			if opAgeExceeded(op, maxAge) {
				_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", "virtual service check failed")
			}
			continue
		}
		if exists {
			_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "succeeded", "")
			continue
		}
		if opAgeExceeded(op, maxAge) {
			_ = platform.UpdateOperationStatus(ctx, client, cfg, tokens, op.ID, "failed", "virtual service missing")
		}
	}
	return nil
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

func evaluateDeploymentStatus(deploy models.DeploymentInfo, op models.OperationPayload, maxAge time.Duration) (bool, bool, string) {
	if deploy.Status.AvailableReplicas > 0 {
		return true, false, ""
	}
	for _, condition := range deploy.Status.Conditions {
		if condition.Type == "Progressing" && strings.EqualFold(condition.Status, "False") {
			if condition.Reason != "" {
				return false, true, condition.Reason
			}
			if condition.Message != "" {
				return false, true, condition.Message
			}
			return false, true, "deployment not progressing"
		}
		if condition.Type == "Available" && strings.EqualFold(condition.Status, "False") {
			if condition.Reason == "ProgressDeadlineExceeded" {
				return false, true, "progress deadline exceeded"
			}
		}
		if condition.Type == "ReplicaFailure" && strings.EqualFold(condition.Status, "True") {
			if condition.Reason != "" {
				return false, true, condition.Reason
			}
			if condition.Message != "" {
				return false, true, condition.Message
			}
			return false, true, "replica failure"
		}
	}
	if opAgeExceeded(op, maxAge) {
		return false, true, "timeout waiting for available replicas"
	}
	return false, false, ""
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

func opAgeExceeded(op models.OperationPayload, maxAge time.Duration) bool {
	if maxAge <= 0 {
		return false
	}
	candidates := []string{op.StartedAt, op.UpdatedAt, op.CreatedAt}
	for _, ts := range candidates {
		if ts == "" {
			continue
		}
		if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
			return time.Since(parsed) > maxAge
		}
	}
	return false
}
