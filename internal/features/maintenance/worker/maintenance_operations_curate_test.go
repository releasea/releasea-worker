package maintenance

import (
	"fmt"
	"releaseaworker/internal/platform/models"
	"testing"
	"time"
)

func TestEvaluateDeploymentStatus(t *testing.T) {
	readyDeploy := models.DeploymentInfo{
		Status: models.DeploymentStatus{AvailableReplicas: 1},
	}
	ready, failed, reason := evaluateDeploymentStatus(readyDeploy, models.OperationPayload{}, time.Minute)
	if !ready || failed || reason != "" {
		t.Fatalf("expected ready deployment, got ready=%v failed=%v reason=%q", ready, failed, reason)
	}

	failedDeploy := models.DeploymentInfo{
		Status: models.DeploymentStatus{
			Conditions: []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}{
				{Type: "ReplicaFailure", Status: "True", Reason: "ImagePullBackOff"},
			},
		},
	}
	ready, failed, reason = evaluateDeploymentStatus(failedDeploy, models.OperationPayload{}, time.Minute)
	if ready || !failed || reason != "ImagePullBackOff" {
		t.Fatalf("expected failure by condition, got ready=%v failed=%v reason=%q", ready, failed, reason)
	}

	oldOperation := models.OperationPayload{
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339),
	}
	ready, failed, reason = evaluateDeploymentStatus(models.DeploymentInfo{}, oldOperation, 5*time.Minute)
	if ready || !failed || reason == "" {
		t.Fatalf("expected timeout-based failure, got ready=%v failed=%v reason=%q", ready, failed, reason)
	}
}

func TestEvaluatePodFailure(t *testing.T) {
	pod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-0"},
		Status: models.PodStatus{
			Phase: "Failed",
		},
	}
	if failed, _ := evaluatePodFailure(pod); !failed {
		t.Fatalf("expected failed phase pod")
	}

	waitingPod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-1"},
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
	if failed, _ := evaluatePodFailure(waitingPod); !failed {
		t.Fatalf("expected waiting failure")
	}

	healthyPod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-2"},
		Status: models.PodStatus{
			ContainerStatuses: []models.ContainerStatus{
				{
					Name:  "api",
					Ready: true,
				},
			},
		},
	}
	if failed, _ := evaluatePodFailure(healthyPod); failed {
		t.Fatalf("did not expect healthy pod failure")
	}
}

func TestWaitingAndTerminatedFailureReason(t *testing.T) {
	failed, reason := waitingFailureReason(&models.ContainerStateWaiting{Reason: "ImagePullBackOff"})
	if !failed || reason != "ImagePullBackOff" {
		t.Fatalf("expected waiting failure, got failed=%v reason=%q", failed, reason)
	}
	failed, _ = waitingFailureReason(&models.ContainerStateWaiting{Reason: "ContainerCreating"})
	if failed {
		t.Fatalf("did not expect failure for ContainerCreating")
	}

	failed, reason = terminatedFailureReason(&models.ContainerStateTerminated{ExitCode: 2, Reason: "Error"})
	if !failed || reason != "Error" {
		t.Fatalf("expected terminated failure, got failed=%v reason=%q", failed, reason)
	}
	failed, reason = terminatedFailureReason(&models.ContainerStateTerminated{ExitCode: 3})
	if !failed || reason != "exit code 3" {
		t.Fatalf("expected fallback exit code reason, got failed=%v reason=%q", failed, reason)
	}
}

func TestOpAgeExceeded(t *testing.T) {
	now := time.Now().UTC()
	opOld := models.OperationPayload{
		StartedAt: now.Add(-20 * time.Minute).Format(time.RFC3339),
	}
	if !opAgeExceeded(opOld, 10*time.Minute) {
		t.Fatalf("expected old operation age exceeded")
	}

	opRecent := models.OperationPayload{
		UpdatedAt: now.Add(-2 * time.Minute).Format(time.RFC3339),
	}
	if opAgeExceeded(opRecent, 10*time.Minute) {
		t.Fatalf("did not expect recent operation age exceeded")
	}

	opInvalid := models.OperationPayload{
		CreatedAt: fmt.Sprintf("%d", now.Unix()),
	}
	if opAgeExceeded(opInvalid, 10*time.Minute) {
		t.Fatalf("did not expect invalid timestamp to exceed age")
	}
}
