package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type runtimeUpdate struct {
	Environment string `json:"environment"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
}

type runtimeIssue struct {
	status   string
	reason   string
	severity int
}

type metricsRequestsPayload struct {
	Requests []float64 `json:"requests"`
}

const (
	defaultRuntimeMonitorSeconds        = 5
	defaultPauseIdleTimeoutSeconds      = 3600
	minPauseIdleTimeoutSeconds          = 60
	maxPauseIdleTimeoutSeconds          = 7 * 24 * 60 * 60
	defaultPauseIdleResumeWindowSeconds = 300
	minPauseIdleResumeWindowSeconds     = 30
)

func runRuntimeMonitor(ctx context.Context, cfg Config, tokens *tokenManager) {
	interval := time.Duration(envInt("WORKER_RUNTIME_SECONDS", defaultRuntimeMonitorSeconds)) * time.Second
	if interval <= 0 {
		interval = time.Duration(defaultRuntimeMonitorSeconds) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	client := &http.Client{Timeout: 15 * time.Second}
	if err := updateRuntimeStatuses(ctx, client, cfg, tokens); err != nil {
		log.Printf("[worker] runtime monitor initial sync error: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := updateRuntimeStatuses(ctx, client, cfg, tokens); err != nil {
				log.Printf("[worker] runtime monitor error: %v", err)
			}
		}
	}
}

func updateRuntimeStatuses(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager) error {
	services, err := fetchServices(ctx, client, cfg, tokens)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return nil
	}

	kubeClient, kubeToken, err := kubeClient()
	if err != nil {
		return err
	}

	environment := strings.TrimSpace(cfg.Environment)
	if environment == "" {
		environment = "prod"
	}
	namespace := resolveNamespace(cfg, environment)

	for _, service := range services {
		if strings.EqualFold(service.Type, "static-site") {
			continue
		}
		serviceName := toKubeName(service.Name)
		if serviceName == "" {
			serviceName = toKubeName(service.ID)
		}
		if serviceName == "" {
			continue
		}

		targets := resolveServicePayloadDeploymentTargets(service, serviceName)
		if len(targets) == 0 {
			targets = []string{serviceName}
		}

		if strings.EqualFold(service.Type, "microservice") && service.PauseOnIdle {
			if handled, status, reason := handlePauseWhenIdle(ctx, client, cfg, tokens, kubeClient, kubeToken, namespace, environment, service, serviceName); handled {
				if status != "" {
					if err := updateServiceRuntime(ctx, client, cfg, tokens, service.ID, environment, status, reason); err != nil {
						log.Printf("[worker] idle runtime update failed service=%s: %v", service.ID, err)
					}
				}
				continue
			}
		}

		status, reason := assessRuntimeTargets(ctx, kubeClient, kubeToken, namespace, targets)
		if status == "" {
			continue
		}
		if err := updateServiceRuntime(ctx, client, cfg, tokens, service.ID, environment, status, reason); err != nil {
			log.Printf("[worker] runtime update failed service=%s: %v", service.ID, err)
		}
	}
	return nil
}

func handlePauseWhenIdle(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	kubeHTTP *http.Client,
	kubeToken string,
	namespace string,
	environment string,
	service servicePayload,
	serviceName string,
) (bool, string, string) {
	idleWindow := resolvePauseIdleWindow(service)
	idleReason := pauseIdleReason(idleWindow)
	resumeWindow := resolvePauseResumeWindow(idleWindow)

	deploy, err := fetchDeployment(ctx, kubeHTTP, kubeToken, namespace, serviceName)
	if err != nil {
		if err == errDeploymentNotFound {
			return false, "", ""
		}
		log.Printf("[worker] idle deploy fetch failed service=%s: %v", service.ID, err)
		return true, "unknown", "Unable to evaluate idle state"
	}

	currentReplicas := deploy.Status.Replicas
	if currentReplicas < 0 {
		currentReplicas = 0
	}

	if currentReplicas == 0 {
		hasTraffic, err := hasRequestActivity(ctx, client, cfg, tokens, service.ID, environment, resumeWindow)
		if err != nil {
			log.Printf("[worker] idle resume traffic check failed service=%s: %v", service.ID, err)
			return true, "idle", idleReason
		}
		if hasTraffic {
			targetReplicas := resumeReplicas(service)
			if err := scaleDeployment(ctx, namespace, serviceName, targetReplicas); err != nil {
				log.Printf("[worker] idle resume failed service=%s replicas=%d: %v", service.ID, targetReplicas, err)
				return true, "unknown", "Failed to resume from idle state"
			}
			return true, "pending", fmt.Sprintf("Resuming from idle state (%d replicas)", targetReplicas)
		}
		return true, "idle", idleReason
	}

	hasTrafficInWindow, err := hasRequestActivity(ctx, client, cfg, tokens, service.ID, environment, idleWindow)
	if err != nil {
		log.Printf("[worker] idle traffic check failed service=%s: %v", service.ID, err)
		return false, "", ""
	}
	if hasTrafficInWindow {
		return false, "", ""
	}

	if err := scaleDeployment(ctx, namespace, serviceName, 0); err != nil {
		log.Printf("[worker] idle pause failed service=%s: %v", service.ID, err)
		return true, "unknown", "Failed to pause after idle period"
	}
	return true, "idle", idleReason
}

func hasRequestActivity(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	serviceID string,
	environment string,
	window time.Duration,
) (bool, error) {
	if window <= 0 {
		window = time.Hour
	}

	now := time.Now().UTC()
	from := now.Add(-window)
	params := url.Values{}
	params.Set("from", from.Format(time.RFC3339))
	params.Set("to", now.Format(time.RFC3339))
	params.Set("environment", normalizeWorkerEnvironment(environment))

	endpoint := fmt.Sprintf("%s/services/%s/metrics?%s", cfg.ApiBaseURL, serviceID, params.Encode())
	var payload metricsRequestsPayload
	if err := doJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		endpoint,
		nil,
		&payload,
		"metrics query",
	); err != nil {
		return false, err
	}
	for _, value := range payload.Requests {
		if value > 0 {
			return true, nil
		}
	}
	return false, nil
}

func resumeReplicas(service servicePayload) int {
	if service.MinReplicas > 0 {
		return service.MinReplicas
	}
	if service.Replicas > 0 {
		return service.Replicas
	}
	return 1
}

func resolvePauseIdleWindow(service servicePayload) time.Duration {
	timeoutSeconds := service.PauseIdleTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = envInt("WORKER_PAUSE_IDLE_DEFAULT_SECONDS", defaultPauseIdleTimeoutSeconds)
	}
	if timeoutSeconds < minPauseIdleTimeoutSeconds {
		timeoutSeconds = minPauseIdleTimeoutSeconds
	}
	if timeoutSeconds > maxPauseIdleTimeoutSeconds {
		timeoutSeconds = maxPauseIdleTimeoutSeconds
	}
	return time.Duration(timeoutSeconds) * time.Second
}

func resolvePauseResumeWindow(idleWindow time.Duration) time.Duration {
	resumeSeconds := envInt("WORKER_PAUSE_IDLE_RESUME_WINDOW_SECONDS", defaultPauseIdleResumeWindowSeconds)
	if resumeSeconds < minPauseIdleResumeWindowSeconds {
		resumeSeconds = minPauseIdleResumeWindowSeconds
	}
	window := time.Duration(resumeSeconds) * time.Second
	if idleWindow > 0 && window > idleWindow {
		return idleWindow
	}
	return window
}

func pauseIdleReason(idleWindow time.Duration) string {
	return fmt.Sprintf("Paused after %s without traffic", formatIdleWindow(idleWindow))
}

func formatIdleWindow(duration time.Duration) string {
	if duration <= 0 {
		return "1 hour"
	}
	minutes := int(duration / time.Minute)
	if duration%time.Minute != 0 {
		minutes++
	}
	if minutes <= 1 {
		return "1 minute"
	}
	if minutes%60 == 0 {
		hours := minutes / 60
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	return fmt.Sprintf("%d minutes", minutes)
}

func fetchServices(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager) ([]servicePayload, error) {
	var services []servicePayload
	err := doJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		cfg.ApiBaseURL+"/services",
		nil,
		&services,
		"services fetch",
	)
	return services, err
}

func updateServiceRuntime(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, serviceID, environment, status, reason string) error {
	payload := runtimeUpdate{
		Environment: environment,
		Status:      status,
		Reason:      reason,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return doJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodPost,
		cfg.ApiBaseURL+"/workers/services/"+serviceID+"/runtime",
		body,
		nil,
		"runtime update",
	)
}

func assessRuntimeTargets(ctx context.Context, client *http.Client, token, namespace string, targets []string) (string, string) {
	worst := runtimeIssue{}
	anyHealthy := false

	for _, target := range targets {
		status, reason := assessRuntime(ctx, client, token, namespace, target)
		if status == "" {
			continue
		}
		if status == "healthy" {
			anyHealthy = true
			continue
		}
		candidate := runtimeIssue{status: status, reason: fmt.Sprintf("%s: %s", target, reason), severity: statusSeverity(status)}
		worst = maxIssue(worst, candidate)
	}

	if worst.severity == 0 {
		if anyHealthy {
			return "healthy", ""
		}
		return "", ""
	}
	if anyHealthy && worst.severity <= statusSeverity("pending") {
		return "degraded", worst.reason
	}
	return worst.status, worst.reason
}

func statusSeverity(status string) int {
	switch status {
	case "healthy":
		return 0
	case "pending":
		return 1
	case "degraded", "unknown":
		return 2
	case "error":
		return 3
	case "crashloop":
		return 4
	default:
		return 1
	}
}

func assessRuntime(ctx context.Context, client *http.Client, token, namespace, serviceName string) (string, string) {
	deploy, err := fetchDeployment(ctx, client, token, namespace, serviceName)
	if err != nil {
		if err == errDeploymentNotFound {
			return "pending", "deployment not found"
		}
		log.Printf("[worker] runtime deploy fetch error: %v", err)
		return "unknown", "deployment check failed"
	}

	pods, err := fetchPodsByLabel(ctx, client, token, namespace, "app="+serviceName)
	if err != nil {
		log.Printf("[worker] runtime pod fetch error: %v", err)
		return "unknown", "pod check failed"
	}

	issue := detectRuntimeIssue(pods)
	if deploy.Status.AvailableReplicas > 0 {
		if issue.severity == 0 {
			return "healthy", ""
		}
		if issue.status == "pending" {
			return "degraded", issue.reason
		}
		return "degraded", issue.reason
	}

	if issue.severity > 0 {
		return issue.status, issue.reason
	}
	return "pending", "waiting for pods"
}

func detectRuntimeIssue(pods podList) runtimeIssue {
	issue := runtimeIssue{}
	if len(pods.Items) == 0 {
		return issue
	}
	for _, pod := range pods.Items {
		issue = maxIssue(issue, inspectPodRuntime(pod))
	}
	return issue
}

func inspectPodRuntime(pod podInfo) runtimeIssue {
	if strings.EqualFold(pod.Status.Phase, "Pending") {
		return runtimeIssue{status: "pending", reason: "pod pending", severity: 1}
	}

	containers := append([]containerStatus{}, pod.Status.InitContainerStatuses...)
	containers = append(containers, pod.Status.ContainerStatuses...)

	for _, status := range containers {
		if status.State.Waiting != nil {
			waiting := status.State.Waiting
			reason := strings.TrimSpace(waiting.Reason)
			switch reason {
			case "CrashLoopBackOff":
				return runtimeIssue{status: "crashloop", reason: reason, severity: 4}
			case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "RegistryUnavailable":
				return runtimeIssue{status: "error", reason: reason, severity: 3}
			case "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
				return runtimeIssue{status: "error", reason: reason, severity: 3}
			case "ContainerCreating", "PodInitializing":
				return runtimeIssue{status: "pending", reason: reason, severity: 1}
			default:
				if reason != "" {
					return runtimeIssue{status: "degraded", reason: reason, severity: 2}
				}
			}
		}
		if status.State.Terminated != nil {
			term := status.State.Terminated
			if term.ExitCode != 0 {
				reason := strings.TrimSpace(term.Reason)
				if reason == "" {
					reason = fmt.Sprintf("exit code %d", term.ExitCode)
				}
				return runtimeIssue{status: "crashloop", reason: reason, severity: 4}
			}
		}
		if status.LastState.Terminated != nil && status.RestartCount > 0 {
			term := status.LastState.Terminated
			if term.ExitCode != 0 {
				reason := strings.TrimSpace(term.Reason)
				if reason == "" {
					reason = fmt.Sprintf("exit code %d", term.ExitCode)
				}
				return runtimeIssue{status: "crashloop", reason: reason, severity: 4}
			}
		}
		if !status.Ready && !strings.EqualFold(status.Name, "istio-proxy") {
			if status.RestartCount > 0 {
				return runtimeIssue{status: "degraded", reason: "container restarting", severity: 2}
			}
			return runtimeIssue{status: "pending", reason: "container starting", severity: 1}
		}
	}

	return runtimeIssue{}
}

func maxIssue(current runtimeIssue, candidate runtimeIssue) runtimeIssue {
	if candidate.severity > current.severity {
		return candidate
	}
	return current
}
