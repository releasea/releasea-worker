package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/platform/models"
	"testing"
)

func TestHandleServiceDeployRegistryWithKubeSimulation(t *testing.T) {
	namespace := "releasea-apps-production"
	var serviceCreated bool
	var deploymentCreated bool
	deleteCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && path == "/workers/credentials":
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:          "svc-1",
					Name:        "api",
					Type:        "microservice",
					SourceType:  "registry",
					DockerImage: "example/releasea/api:test",
				},
			})
			return
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
			return
		case r.Method == http.MethodPatch && path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
			return
		case r.Method == http.MethodGet && path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api":
			if !deploymentCreated {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
					Replicas:          1,
				},
			})
			return
		case r.Method == http.MethodPost && path == "/apis/apps/v1/namespaces/"+namespace+"/deployments":
			deploymentCreated = true
			w.WriteHeader(http.StatusCreated)
			return
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace+"/services/api":
			if !serviceCreated {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api"},
				},
			})
			return
		case r.Method == http.MethodPost && path == "/api/v1/namespaces/"+namespace+"/services":
			serviceCreated = true
			w.WriteHeader(http.StatusCreated)
			return
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
			return
		case r.Method == http.MethodGet && path == "/apis/networking.istio.io/v1beta1/namespaces/"+namespace+"/virtualservices":
			w.WriteHeader(http.StatusNotFound)
			return
		case r.Method == http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNotFound)
			return
		default:
			http.NotFound(w, r)
			return
		}
	}))
	defer server.Close()

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("kube-token"), 0o600); err != nil {
		t.Fatalf("failed to write kube token file: %v", err)
	}

	t.Setenv("RELEASEA_KUBE_API_BASE_URL", server.URL)
	t.Setenv("RELEASEA_KUBE_TOKEN_FILE", tokenFile)
	t.Setenv("RELEASEA_KUBE_INSECURE_SKIP_VERIFY", "true")

	cfg := models.Config{
		ApiBaseURL:      server.URL,
		InternalDomain:  "internal.example",
		ExternalDomain:  "external.example",
		InternalGateway: "istio-system/internal",
		ExternalGateway: "istio-system/external",
	}
	tokens := platform.NewTokenManager("access-token")
	op := models.OperationPayload{
		ID:       "op-1",
		Type:     "service.deploy",
		Resource: "svc-1",
		Status:   "queued",
		Payload: map[string]interface{}{
			"environment": "prod",
			"resources": []interface{}{
				map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name": "api",
					},
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{},
							"spec": map[string]interface{}{
								"containers": []interface{}{
									map[string]interface{}{"name": "api"},
								},
							},
						},
					},
				},
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name": "api",
					},
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{"app": "api"},
					},
				},
			},
		},
	}

	if err := HandleServiceDeploy(context.Background(), server.Client(), cfg, tokens, op); err != nil {
		t.Fatalf("unexpected handle service deploy error: %v", err)
	}
	if !serviceCreated || !deploymentCreated {
		t.Fatalf("expected deployment and service created, got deployment=%v service=%v", deploymentCreated, serviceCreated)
	}
	if deleteCalls == 0 {
		t.Fatalf("expected shadow cleanup delete calls for rolling strategy")
	}
}

func TestHandleServiceDeployValidationPaths(t *testing.T) {
	err := HandleServiceDeploy(context.Background(), &http.Client{}, models.Config{}, platform.NewTokenManager("token"), models.OperationPayload{
		Resource: "svc",
		Payload:  map[string]interface{}{"environment": "prod"},
	})
	if err == nil {
		t.Fatalf("expected validation/runtime failure path")
	}
}
