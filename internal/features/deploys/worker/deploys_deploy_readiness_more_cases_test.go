package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/platform/models"
	"strings"
	"testing"
	"time"
)

func TestDeploymentFailureReasonAdditionalCases(t *testing.T) {
	failed, reason := deploymentFailureReason(models.DeploymentInfo{
		Status: models.DeploymentStatus{
			Conditions: []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}{
				{Type: "Progressing", Status: "False", Message: "still progressing"},
			},
		},
	})
	if !failed || reason != "still progressing" {
		t.Fatalf("expected message failure for progressing=false, got failed=%v reason=%q", failed, reason)
	}

	failed, reason = deploymentFailureReason(models.DeploymentInfo{
		Status: models.DeploymentStatus{
			Conditions: []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}{
				{Type: "Available", Status: "False", Reason: "ProgressDeadlineExceeded"},
			},
		},
	})
	if !failed || reason != "progress deadline exceeded" {
		t.Fatalf("expected progress deadline failure, got failed=%v reason=%q", failed, reason)
	}

	failed, reason = deploymentFailureReason(models.DeploymentInfo{
		Status: models.DeploymentStatus{
			Conditions: []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}{
				{Type: "ReplicaFailure", Status: "True", Message: "replica failed to start"},
			},
		},
	})
	if !failed || reason != "replica failed to start" {
		t.Fatalf("expected replica failure message, got failed=%v reason=%q", failed, reason)
	}
}

func TestFetchDeploymentAndPodsErrorBranches(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/deployments/"):
			w.WriteHeader(http.StatusInternalServerError)
		case strings.Contains(r.URL.Path, "/pods"):
			_, _ = w.Write([]byte("{invalid-json"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("RELEASEA_KUBE_API_BASE_URL", server.URL)

	if _, err := fetchDeployment(context.Background(), server.Client(), "token", "apps", "api"); err == nil {
		t.Fatalf("expected deployment fetch error for status >= 400")
	}
	if _, err := fetchPodsByLabel(context.Background(), server.Client(), "token", "apps", "app=api"); err == nil {
		t.Fatalf("expected pods decode error for invalid payload")
	}
}

func TestWaitForServiceDeployReadinessPendingThenContextCancel(t *testing.T) {
	namespace := "releasea-apps-production"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api":
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 0,
					Replicas:          1,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
		case r.Method == http.MethodPost && r.URL.Path == "/deploys/dep-ready/logs":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	t.Setenv("WORKER_DEPLOY_READY_POLL_SECONDS", "1")

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	logger := platform.NewDeployLogger(server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("access-token"), "dep-ready")
	err := waitForServiceDeployReadiness(
		ctx,
		models.Config{ApiBaseURL: server.URL},
		"prod",
		namespace,
		"api",
		[]string{"api"},
		models.ServiceConfig{},
		logger,
	)
	if err == nil {
		t.Fatalf("expected context cancellation while waiting for readiness")
	}
}
