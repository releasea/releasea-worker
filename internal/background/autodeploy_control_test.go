package background

import (
	"reflect"
	"releaseaworker/internal/models"
	"testing"
	"time"
)

func boolPtr(value bool) *bool {
	return &value
}

func TestAutoDeployRuntimeTrackers(t *testing.T) {
	runtime := &autoDeployMonitorRuntime{
		recentlyQueued:      map[string]time.Time{},
		serviceCooldownByID: map[string]time.Time{},
	}
	now := time.Now().UTC()

	runtime.markQueued("svc|prod|sha", now.Add(30*time.Second))
	if !runtime.wasRecentlyQueued("svc|prod|sha", now) {
		t.Fatalf("expected recently queued item")
	}

	runtime.markServiceCooldown("svc", now.Add(45*time.Second))
	if !runtime.inServiceCooldown("svc", now) {
		t.Fatalf("expected service cooldown active")
	}

	runtime.evictExpired(now.Add(60 * time.Second))
	if runtime.wasRecentlyQueued("svc|prod|sha", now.Add(60*time.Second)) {
		t.Fatalf("expected queued item evicted after expiration")
	}
	if runtime.inServiceCooldown("svc", now.Add(60*time.Second)) {
		t.Fatalf("expected service cooldown evicted after expiration")
	}
}

func TestResolveAutoDeployTimings(t *testing.T) {
	t.Setenv("WORKER_AUTODEPLOY_LEASE_SECONDS", "10")
	if got := resolveAutoDeployLeaseTTL(20 * time.Second); got != 30 {
		t.Fatalf("expected lease ttl minimum 30, got %d", got)
	}

	t.Setenv("WORKER_AUTODEPLOY_LEASE_SECONDS", "999")
	if got := resolveAutoDeployLeaseTTL(20 * time.Second); got != 600 {
		t.Fatalf("expected lease ttl capped at 600, got %d", got)
	}

	t.Setenv("WORKER_AUTODEPLOY_ERROR_COOLDOWN_SECONDS", "5")
	if got := resolveAutoDeployErrorCooldown(30 * time.Second); got != 20*time.Second {
		t.Fatalf("expected min error cooldown 20s, got %s", got)
	}

	t.Setenv("WORKER_AUTODEPLOY_QUEUE_ERROR_COOLDOWN_SECONDS", "2")
	if got := resolveAutoDeployQueueErrorCooldown(30 * time.Second); got != 10*time.Second {
		t.Fatalf("expected min queue cooldown 10s, got %s", got)
	}

	t.Setenv("WORKER_AUTODEPLOY_PENDING_SECONDS", "5")
	if got := resolveAutoDeployRecentQueueTTL(30 * time.Second); got != 30*time.Second {
		t.Fatalf("expected min pending ttl 30s, got %s", got)
	}
}

func TestShouldAutoDeployService(t *testing.T) {
	service := models.ServicePayload{
		SourceType: "git",
		RepoURL:    "https://github.com/releasea/platform",
	}
	if !shouldAutoDeployService(service) {
		t.Fatalf("expected auto deploy enabled by default")
	}

	service.AutoDeploy = boolPtr(false)
	if shouldAutoDeployService(service) {
		t.Fatalf("expected disabled when autoDeploy=false")
	}

	service.AutoDeploy = boolPtr(true)
	service.DeployTemplateID = "tpl-cronjob"
	if shouldAutoDeployService(service) {
		t.Fatalf("expected disabled for cronjob")
	}

	service.DeployTemplateID = ""
	service.Status = "deleting"
	if shouldAutoDeployService(service) {
		t.Fatalf("expected disabled for deleting service")
	}

	service.Status = "active"
	service.RepoURL = ""
	if shouldAutoDeployService(service) {
		t.Fatalf("expected disabled without repository")
	}

	service.RepoURL = "docker.io/releasea/api"
	service.SourceType = "registry"
	if shouldAutoDeployService(service) {
		t.Fatalf("expected disabled for registry source")
	}
}

func TestBuildAutoDeployStates(t *testing.T) {
	deploys := []deploySnapshot{
		{
			ServiceID:   "svc-1",
			Environment: "production",
			Commit:      "ABCDEF",
			Status:      "queued",
		},
		{
			ServiceID:   "svc-1",
			Environment: "prod",
			Commit:      "abcdef",
			Status:      "completed",
		},
		{
			ServiceID:   "svc-2",
			Environment: "dev",
			Commit:      "",
			Status:      "success",
		},
		{
			ServiceID: "",
		},
	}

	states := buildAutoDeployStates(deploys)
	state1 := states["svc-1|prod"]
	if state1 == nil {
		t.Fatalf("expected state for svc-1|prod")
	}
	if !state1.blocking {
		t.Fatalf("expected blocking state for queued deploy")
	}
	if _, ok := state1.commits["abcdef"]; !ok {
		t.Fatalf("expected normalized commit in state")
	}

	state2 := states["svc-2|dev"]
	if state2 == nil {
		t.Fatalf("expected state for svc-2|dev")
	}
	if state2.blocking {
		t.Fatalf("did not expect blocking for completed deploy")
	}
}

func TestParseAndNormalizeHelpers(t *testing.T) {
	owner, repo, ok := parseGitHubRepository("https://github.com/releasea/worker.git")
	if !ok || owner != "releasea" || repo != "worker" {
		t.Fatalf("unexpected parse result owner=%q repo=%q ok=%v", owner, repo, ok)
	}

	if _, _, ok := parseGitHubRepository("git@github.com:releasea/worker.git"); ok {
		t.Fatalf("ssh format should not match current parser")
	}

	if got := normalizeCommitSHA(" AbCd "); got != "abcd" {
		t.Fatalf("unexpected normalized sha: %q", got)
	}

	if got := normalizeDeployStatus("in-progress"); got != "deploying" {
		t.Fatalf("expected deploying, got %q", got)
	}
	if !isAutoDeployBlockingStatus("retrying") || isAutoDeployBlockingStatus("completed") {
		t.Fatalf("unexpected blocking status evaluation")
	}
	if got := normalizeWorkerEnvironment("production"); got != "prod" {
		t.Fatalf("expected prod environment, got %q", got)
	}
	if got := normalizeWorkerEnvironment("sandbox"); got != "dev" {
		t.Fatalf("expected dev environment, got %q", got)
	}
	if got := autoDeployStateKey(" service ", "production"); got != "service|prod" {
		t.Fatalf("unexpected state key: %q", got)
	}

	got := []string{normalizeWorkerEnvironment("prod"), normalizeWorkerEnvironment("staging"), normalizeWorkerEnvironment("dev")}
	want := []string{"prod", "staging", "dev"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}
