package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/models"
	"testing"
)

func TestDeployReadinessHTTPHelpers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/apps/deployments/api":
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/apps/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{
				Items: []models.PodInfo{
					{
						Metadata: models.PodMetadata{Name: "api-0"},
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
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := kubeRewriteClient(server)
	deployment, err := fetchDeployment(context.Background(), client, "token", "apps", "api")
	if err != nil {
		t.Fatalf("unexpected deployment fetch error: %v", err)
	}
	if deployment.Status.AvailableReplicas != 1 {
		t.Fatalf("expected available replicas=1, got %d", deployment.Status.AvailableReplicas)
	}

	pods, err := fetchPodsByLabel(context.Background(), client, "token", "apps", "app=api")
	if err != nil {
		t.Fatalf("unexpected pod fetch error: %v", err)
	}
	if len(pods.Items) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods.Items))
	}

	failed, reason := evaluateDeploymentPods(context.Background(), client, "token", "apps", "api")
	if !failed || reason == "" {
		t.Fatalf("expected pod failure detection, got failed=%v reason=%q", failed, reason)
	}
}

func TestFetchDeploymentNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := kubeRewriteClient(server)
	_, err := fetchDeployment(context.Background(), client, "token", "apps", "missing")
	if err != errDeploymentNotFound {
		t.Fatalf("expected errDeploymentNotFound, got %v", err)
	}
}

func TestStaticSiteCommandHelpersRuntimeErrors(t *testing.T) {
	if err := ensureMinioBucket(context.Background(), "alias", "bucket", nil); err == nil {
		t.Fatalf("expected runtime error from mc bucket command")
	}
	if err := setBucketPublic(context.Background(), "alias", "bucket", nil); err == nil {
		t.Fatalf("expected runtime error from mc policy command")
	}
	if err := mirrorStaticSite(context.Background(), "alias", "bucket", "prefix", ".", "public, max-age=60", nil); err == nil {
		t.Fatalf("expected runtime error from mc mirror command")
	}
}

func TestDeployHandlersValidationErrors(t *testing.T) {
	if err := HandleServiceDeploy(context.Background(), &http.Client{}, models.Config{}, nil, models.OperationPayload{}); err == nil {
		t.Fatalf("expected service deploy validation error")
	}
	if err := HandlePromoteCanary(context.Background(), &http.Client{}, models.Config{}, nil, models.OperationPayload{}); err == nil {
		t.Fatalf("expected canary promote validation error")
	}
}

func TestPromoteBlueGreenKubeClientError(t *testing.T) {
	_, err := promoteBlueGreen(
		context.Background(),
		&http.Client{},
		models.Config{},
		nil,
		models.ServiceConfig{
			DeploymentStrategy: models.DeploymentStrategyConfig{BlueGreenPrimary: "blue"},
		},
		"svc-1",
		"prod",
		"api",
		nil,
	)
	if err == nil {
		t.Fatalf("expected kube client error in local test environment")
	}
}

func TestApplyServiceWorkloadResourcesWrapper(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// delete HPA path for rolling with autoscaling disabled will return not found and be tolerated.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := kubeRewriteClient(server)
	err := applyServiceWorkloadResources(
		context.Background(),
		client,
		"token",
		"apps",
		"api",
		nil,
		models.ServiceConfig{
			DeploymentStrategy: models.DeploymentStrategyConfig{Type: "rolling"},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected wrapper apply error: %v", err)
	}
}

func TestStampDeployRevisionKubectlNoop(t *testing.T) {
	stampDeployRevisionKubectl(context.Background(), models.Config{}, "prod", "", models.ServiceConfig{}, nil)
}
