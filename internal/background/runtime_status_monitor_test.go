package background

import (
	"releaseaworker/internal/models"
	"strings"
	"testing"
	"time"
)

func TestResumeReplicas(t *testing.T) {
	if got := resumeReplicas(models.ServicePayload{MinReplicas: 3, Replicas: 1}); got != 3 {
		t.Fatalf("expected min replicas priority, got %d", got)
	}
	if got := resumeReplicas(models.ServicePayload{Replicas: 2}); got != 2 {
		t.Fatalf("expected replicas fallback, got %d", got)
	}
	if got := resumeReplicas(models.ServicePayload{}); got != 1 {
		t.Fatalf("expected default 1 replica, got %d", got)
	}
}

func TestResolvePauseIdleWindow(t *testing.T) {
	t.Setenv("WORKER_PAUSE_IDLE_DEFAULT_SECONDS", "120")
	if got := resolvePauseIdleWindow(models.ServicePayload{}); got != 120*time.Second {
		t.Fatalf("expected default idle window 120s, got %s", got)
	}

	if got := resolvePauseIdleWindow(models.ServicePayload{PauseIdleTimeoutSeconds: 10}); got != minPauseIdleTimeoutSeconds*time.Second {
		t.Fatalf("expected min idle window clamp, got %s", got)
	}
	if got := resolvePauseIdleWindow(models.ServicePayload{PauseIdleTimeoutSeconds: maxPauseIdleTimeoutSeconds + 1}); got != maxPauseIdleTimeoutSeconds*time.Second {
		t.Fatalf("expected max idle window clamp, got %s", got)
	}
}

func TestResolvePauseResumeWindow(t *testing.T) {
	t.Setenv("WORKER_PAUSE_IDLE_RESUME_WINDOW_SECONDS", "20")
	if got := resolvePauseResumeWindow(5 * time.Minute); got != minPauseIdleResumeWindowSeconds*time.Second {
		t.Fatalf("expected min resume window clamp, got %s", got)
	}

	t.Setenv("WORKER_PAUSE_IDLE_RESUME_WINDOW_SECONDS", "600")
	if got := resolvePauseResumeWindow(2 * time.Minute); got != 2*time.Minute {
		t.Fatalf("expected resume window capped by idle window, got %s", got)
	}
}

func TestFormatIdleWindowAndReason(t *testing.T) {
	if got := formatIdleWindow(0); got != "1 hour" {
		t.Fatalf("expected default 1 hour, got %q", got)
	}
	if got := formatIdleWindow(30 * time.Second); got != "1 minute" {
		t.Fatalf("expected 1 minute rounding, got %q", got)
	}
	if got := formatIdleWindow(2 * time.Hour); got != "2 hours" {
		t.Fatalf("expected 2 hours, got %q", got)
	}
	if got := formatIdleWindow(95 * time.Minute); got != "95 minutes" {
		t.Fatalf("expected 95 minutes, got %q", got)
	}
	reason := pauseIdleReason(15 * time.Minute)
	if !strings.Contains(reason, "15 minutes") {
		t.Fatalf("expected idle reason with formatted duration, got %q", reason)
	}
}

func TestStatusSeverityAndMaxIssue(t *testing.T) {
	if statusSeverity("healthy") != 0 || statusSeverity("crashloop") != 4 {
		t.Fatalf("unexpected severity mapping")
	}

	current := runtimeIssue{status: "pending", severity: 1}
	candidate := runtimeIssue{status: "error", severity: 3}
	got := maxIssue(current, candidate)
	if got.status != "error" {
		t.Fatalf("expected higher severity issue selected, got %+v", got)
	}
}

func TestInspectAndDetectRuntimeIssue(t *testing.T) {
	pendingPod := models.PodInfo{
		Status: models.PodStatus{Phase: "Pending"},
	}
	issue := inspectPodRuntime(pendingPod)
	if issue.status != "pending" {
		t.Fatalf("expected pending status, got %+v", issue)
	}

	crashLoopPod := models.PodInfo{
		Status: models.PodStatus{
			ContainerStatuses: []models.ContainerStatus{
				{
					Name: "api",
					State: models.ContainerState{
						Waiting: &models.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
					},
				},
			},
		},
	}
	issue = inspectPodRuntime(crashLoopPod)
	if issue.status != "crashloop" {
		t.Fatalf("expected crashloop issue, got %+v", issue)
	}

	pods := models.PodList{Items: []models.PodInfo{pendingPod, crashLoopPod}}
	detected := detectRuntimeIssue(pods)
	if detected.status != "crashloop" {
		t.Fatalf("expected worst issue crashloop, got %+v", detected)
	}
}
