package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	platformauth "releaseaworker/internal/platform/auth"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
	"time"
)

type deploySnapshot struct {
	ServiceID   string `json:"serviceId"`
	Environment string `json:"environment"`
	Commit      string `json:"commit"`
	Status      string `json:"status"`
}

type scmCommitEntry struct {
	Sha string `json:"sha"`
}

type autoDeployQueueResponse struct {
	Deploy struct {
		Commit string `json:"commit"`
	} `json:"deploy"`
	Queued                *bool `json:"queued"`
	BlockedByActiveDeploy bool  `json:"blockedByActiveDeploy"`
}

type autoDeployState struct {
	blocking bool
	commits  map[string]struct{}
}

type autoDeployLeaseResponse struct {
	Granted   bool   `json:"granted"`
	Holder    string `json:"holder"`
	ExpiresAt string `json:"expiresAt"`
}

type autoDeployMonitorRuntime struct {
	recentlyQueued      map[string]time.Time
	serviceCooldownByID map[string]time.Time
}

func RunAutoDeployMonitor(ctx context.Context, cfg models.Config, tokens *platformauth.TokenManager) {
	if shared.EnvInt("WORKER_AUTODEPLOY_ENABLED", 1) == 0 {
		log.Printf("[worker] auto deploy monitor disabled")
		return
	}

	interval := time.Duration(shared.EnvInt("WORKER_AUTODEPLOY_SECONDS", 60)) * time.Second
	if interval < 20*time.Second {
		interval = 20 * time.Second
	}
	leaseTTLSeconds := resolveAutoDeployLeaseTTL(interval)
	runtime := &autoDeployMonitorRuntime{
		recentlyQueued:      make(map[string]time.Time),
		serviceCooldownByID: make(map[string]time.Time),
	}

	client := &http.Client{Timeout: 20 * time.Second}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	runCycle := func() {
		cycleCtx, cancel := context.WithTimeout(ctx, resolveMaintenanceCycleTimeout(interval))
		defer cancel()
		if err := runAutoDeployCycle(cycleCtx, client, cfg, tokens, runtime, interval, leaseTTLSeconds); err != nil {
			log.Printf("[worker] auto deploy monitor error: %v", err)
		}
	}

	runCycle()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCycle()
		}
	}
}

func runAutoDeployCycle(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	runtime *autoDeployMonitorRuntime,
	interval time.Duration,
	leaseTTLSeconds int,
) error {
	environment := normalizeWorkerEnvironment(cfg.Environment)
	granted, _, err := acquireAutoDeployLease(ctx, client, cfg, tokens, environment, leaseTTLSeconds)
	if err != nil {
		return err
	}
	if !granted {
		return nil
	}

	now := time.Now().UTC()
	runtime.evictExpired(now)

	services, err := fetchAutoDeployServices(ctx, client, cfg, tokens)
	if err != nil {
		return err
	}
	if len(services) == 0 {
		return nil
	}

	deploys, err := fetchDeploySnapshots(ctx, client, cfg, tokens)
	if err != nil {
		return err
	}

	states := buildAutoDeployStates(deploys)

	for _, service := range services {
		if !shouldAutoDeployService(service) {
			continue
		}

		stateKey := autoDeployStateKey(service.ID, environment)
		state := states[stateKey]
		if state != nil && state.blocking {
			continue
		}

		if runtime.inServiceCooldown(service.ID, now) {
			continue
		}

		latestCommit, err := fetchLatestServiceCommit(ctx, client, cfg, tokens, service)
		if err != nil {
			runtime.markServiceCooldown(service.ID, now.Add(resolveAutoDeployErrorCooldown(interval)))
			log.Printf("[worker] auto deploy commit lookup failed service=%s: %v", service.ID, err)
			continue
		}
		runtime.clearServiceCooldown(service.ID)

		latestCommit = normalizeCommitSHA(latestCommit)
		if latestCommit == "" {
			continue
		}

		if state != nil {
			if _, exists := state.commits[latestCommit]; exists {
				continue
			}
		}

		queueKey := stateKey + "|" + latestCommit
		if runtime.wasRecentlyQueued(queueKey, now) {
			continue
		}

		queued, err := queueAutoDeploy(ctx, client, cfg, tokens, service.ID, environment, latestCommit)
		if err != nil {
			runtime.markServiceCooldown(service.ID, now.Add(resolveAutoDeployQueueErrorCooldown(interval)))
			log.Printf("[worker] auto deploy queue failed service=%s commit=%s: %v", service.ID, latestCommit, err)
			continue
		}
		if !queued {
			continue
		}
		runtime.markQueued(queueKey, now.Add(resolveAutoDeployRecentQueueTTL(interval)))
		if state == nil {
			state = &autoDeployState{commits: make(map[string]struct{})}
			states[stateKey] = state
		}
		state.commits[latestCommit] = struct{}{}
		log.Printf("[worker] auto deploy queued service=%s env=%s commit=%s", service.ID, environment, latestCommit)
	}

	return nil
}

func (runtime *autoDeployMonitorRuntime) evictExpired(now time.Time) {
	for key, expiresAt := range runtime.recentlyQueued {
		if !expiresAt.After(now) {
			delete(runtime.recentlyQueued, key)
		}
	}
	for serviceID, cooldownUntil := range runtime.serviceCooldownByID {
		if !cooldownUntil.After(now) {
			delete(runtime.serviceCooldownByID, serviceID)
		}
	}
}

func (runtime *autoDeployMonitorRuntime) markQueued(key string, until time.Time) {
	if strings.TrimSpace(key) == "" {
		return
	}
	runtime.recentlyQueued[key] = until
}

func (runtime *autoDeployMonitorRuntime) wasRecentlyQueued(key string, now time.Time) bool {
	expiresAt, ok := runtime.recentlyQueued[key]
	return ok && expiresAt.After(now)
}

func (runtime *autoDeployMonitorRuntime) markServiceCooldown(serviceID string, until time.Time) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return
	}
	runtime.serviceCooldownByID[serviceID] = until
}

func (runtime *autoDeployMonitorRuntime) clearServiceCooldown(serviceID string) {
	delete(runtime.serviceCooldownByID, strings.TrimSpace(serviceID))
}

func (runtime *autoDeployMonitorRuntime) inServiceCooldown(serviceID string, now time.Time) bool {
	expiresAt, ok := runtime.serviceCooldownByID[strings.TrimSpace(serviceID)]
	return ok && expiresAt.After(now)
}

func resolveAutoDeployLeaseTTL(interval time.Duration) int {
	defaultTTL := int(interval.Seconds()*2) + 15
	ttl := shared.EnvInt("WORKER_AUTODEPLOY_LEASE_SECONDS", defaultTTL)
	if ttl < 30 {
		ttl = 30
	}
	if ttl > 600 {
		ttl = 600
	}
	return ttl
}

func resolveAutoDeployErrorCooldown(interval time.Duration) time.Duration {
	seconds := shared.EnvInt("WORKER_AUTODEPLOY_ERROR_COOLDOWN_SECONDS", int(interval.Seconds()))
	if seconds < 20 {
		seconds = 20
	}
	if seconds > 900 {
		seconds = 900
	}
	return time.Duration(seconds) * time.Second
}

func resolveAutoDeployQueueErrorCooldown(interval time.Duration) time.Duration {
	seconds := shared.EnvInt("WORKER_AUTODEPLOY_QUEUE_ERROR_COOLDOWN_SECONDS", int(interval.Seconds()/2))
	if seconds < 10 {
		seconds = 10
	}
	if seconds > 300 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func resolveAutoDeployRecentQueueTTL(interval time.Duration) time.Duration {
	seconds := shared.EnvInt("WORKER_AUTODEPLOY_PENDING_SECONDS", int(interval.Seconds()*2)+10)
	if seconds < 30 {
		seconds = 30
	}
	if seconds > 900 {
		seconds = 900
	}
	return time.Duration(seconds) * time.Second
}

func shouldAutoDeployService(service models.ServicePayload) bool {
	autoDeployEnabled := service.AutoDeploy == nil || *service.AutoDeploy
	if !autoDeployEnabled {
		return false
	}
	if strings.EqualFold(service.DeployTemplateID, "tpl-cronjob") {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(service.Status))
	if status == "deleting" {
		return false
	}
	if strings.TrimSpace(service.RepoURL) == "" {
		return false
	}
	sourceType := shared.NormalizeSourceType(service.SourceType)
	return sourceType != "registry"
}

func fetchAutoDeployServices(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager) ([]models.ServicePayload, error) {
	var services []models.ServicePayload
	err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		cfg.ApiBaseURL+"/services?autoDeploy=true&excludeStatus=deleting",
		nil,
		&services,
		"services fetch",
	)
	return services, err
}

func fetchDeploySnapshots(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager) ([]deploySnapshot, error) {
	var deploys []deploySnapshot
	err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		cfg.ApiBaseURL+"/deploys",
		nil,
		&deploys,
		"deploys fetch",
	)
	return deploys, err
}

func buildAutoDeployStates(deploys []deploySnapshot) map[string]*autoDeployState {
	states := make(map[string]*autoDeployState)
	for _, deploy := range deploys {
		serviceID := strings.TrimSpace(deploy.ServiceID)
		if serviceID == "" {
			continue
		}
		env := normalizeWorkerEnvironment(deploy.Environment)
		key := autoDeployStateKey(serviceID, env)
		state := states[key]
		if state == nil {
			state = &autoDeployState{commits: make(map[string]struct{})}
			states[key] = state
		}

		status := normalizeDeployStatus(deploy.Status)
		if isAutoDeployBlockingStatus(status) {
			state.blocking = true
		}

		commit := normalizeCommitSHA(deploy.Commit)
		if commit != "" {
			state.commits[commit] = struct{}{}
		}
	}
	return states
}

func fetchLatestServiceCommit(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	service models.ServicePayload,
) (string, error) {
	branch := strings.TrimSpace(service.Branch)
	if branch == "" {
		branch = "main"
	}

	params := url.Values{}
	params.Set("repoUrl", strings.TrimSpace(service.RepoURL))
	params.Set("branch", branch)
	if projectID := strings.TrimSpace(service.ProjectID); projectID != "" {
		params.Set("projectId", projectID)
	}
	if scmCredentialID := strings.TrimSpace(service.SCMCredentialID); scmCredentialID != "" {
		params.Set("scmCredentialId", scmCredentialID)
	}

	endpoint := cfg.ApiBaseURL + "/scm/commits?" + params.Encode()
	var commits []scmCommitEntry
	if err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		endpoint,
		nil,
		&commits,
		"commit lookup",
	); err != nil {
		return "", err
	}
	if len(commits) == 0 {
		return "", nil
	}
	return commits[0].Sha, nil
}

func acquireAutoDeployLease(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	environment string,
	ttlSeconds int,
) (bool, string, error) {
	payload := map[string]interface{}{
		"holder":      strings.TrimSpace(cfg.WorkerID),
		"environment": environment,
		"ttlSeconds":  ttlSeconds,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, "", err
	}

	var lease autoDeployLeaseResponse
	if err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodPost,
		cfg.ApiBaseURL+"/workers/autodeploy/lease",
		body,
		&lease,
		"auto deploy lease",
	); err != nil {
		return false, "", err
	}
	return lease.Granted, strings.TrimSpace(lease.Holder), nil
}

func queueAutoDeploy(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	serviceID string,
	environment string,
	commit string,
) (bool, error) {
	payload := map[string]string{
		"environment": environment,
		"version":     commit,
		"trigger":     "auto",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}

	endpoint := fmt.Sprintf("%s/services/%s/deploys", cfg.ApiBaseURL, serviceID)
	var response autoDeployQueueResponse
	if err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodPost,
		endpoint,
		body,
		&response,
		"auto deploy",
	); err != nil {
		return false, err
	}

	requestedCommit := normalizeCommitSHA(commit)
	responseCommit := normalizeCommitSHA(response.Deploy.Commit)
	if requestedCommit == "" {
		return true, nil
	}
	if responseCommit == "" {
		return true, nil
	}
	if responseCommit != requestedCommit {
		if response.BlockedByActiveDeploy || (response.Queued != nil && !*response.Queued) {
			return false, nil
		}
	}
	return responseCommit == requestedCommit, nil
}

func normalizeCommitSHA(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizeDeployStatus(status string) string {
	value := strings.ToLower(strings.TrimSpace(status))
	switch value {
	case "queued":
		return "scheduled"
	case "in-progress":
		return "deploying"
	case "success":
		return "completed"
	default:
		return value
	}
}

func isAutoDeployBlockingStatus(status string) bool {
	switch status {
	case "requested", "scheduled", "preparing", "deploying", "retrying":
		return true
	default:
		return false
	}
}

func normalizeWorkerEnvironment(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "prod", "production", "live":
		return "prod"
	case "staging", "stage", "pre-prod", "preprod", "uat":
		return "staging"
	case "dev", "development", "qa", "sandbox", "test", "testing", "preview", "local":
		return "dev"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func autoDeployStateKey(serviceID, environment string) string {
	return strings.TrimSpace(serviceID) + "|" + normalizeWorkerEnvironment(environment)
}
