package deploy

import (
	"releaseaworker/internal/platform/models"
	"strings"
	"testing"
)

func TestRolloutPendingReasonRequiresFullReplacement(t *testing.T) {
	partial := settledDeployment(2)
	partial.Status.UpdatedReplicas = 1
	partial.Status.Replicas = 3
	if reason := rolloutPendingReason("api", partial); !strings.Contains(reason, "still updating") {
		t.Fatalf("expected an in-progress rollout to stay unfinished, got %q", reason)
	}

	finished := settledDeployment(2)
	if reason := rolloutPendingReason("api", finished); reason != "" {
		t.Fatalf("expected a fully replaced deployment to be finished, got %q", reason)
	}
}

func TestRolloutPendingReasonWaitsForPreviousPods(t *testing.T) {
	deploy := settledDeployment(1)
	deploy.Status.Replicas = 2
	deploy.Status.UnavailableReplicas = 1
	reason := rolloutPendingReason("api", deploy)
	if !strings.Contains(reason, "previous pods") {
		t.Fatalf("expected previous pods to block completion, got %q", reason)
	}
}

func TestLivePodCountIgnoresFinishedPods(t *testing.T) {
	pods := models.PodList{Items: []models.PodInfo{
		{Status: models.PodStatus{Phase: "Succeeded"}},
		{Status: models.PodStatus{Phase: "Running"}},
	}}
	if livePodCount(pods) != 1 {
		t.Fatalf("expected one live pod, got %d", livePodCount(pods))
	}
}
