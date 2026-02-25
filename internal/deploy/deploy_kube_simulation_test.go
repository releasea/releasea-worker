package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"
	"testing"
)

func setupKubeEnvForTest(t *testing.T, kubeBaseURL string) {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("kube-token"), 0o600); err != nil {
		t.Fatalf("failed to write token file: %v", err)
	}
	t.Setenv("RELEASEA_KUBE_API_BASE_URL", kubeBaseURL)
	t.Setenv("RELEASEA_KUBE_TOKEN_FILE", tokenFile)
	t.Setenv("RELEASEA_KUBE_INSECURE_SKIP_VERIFY", "true")
}

func TestApplyDeployResourcesWithKubeSimulation(t *testing.T) {
	namespace := "releasea-apps-production"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			ID:          "svc-1",
			Name:        "api",
			DockerImage: "example/releasea/api:test",
			SourceType:  "registry",
			Port:        8080,
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type: "rolling",
			},
			Environment: map[string]string{
				"MODE": "prod",
			},
		},
		Template: &models.DeployTemplate{
			Resources: []map[string]interface{}{
				map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":      "{{serviceName}}",
						"namespace": "{{namespace}}",
					},
					"spec": map[string]interface{}{
						"template": map[string]interface{}{
							"metadata": map[string]interface{}{},
							"spec": map[string]interface{}{
								"containers": []interface{}{
									map[string]interface{}{
										"name":  "app",
										"image": "{{image}}",
									},
								},
							},
						},
					},
				},
				map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Service",
					"metadata": map[string]interface{}{
						"name":      "{{serviceName}}",
						"namespace": "{{namespace}}",
					},
					"spec": map[string]interface{}{
						"selector": map[string]interface{}{
							"app": "{{serviceName}}",
						},
					},
				},
			},
		},
	}

	err := applyDeployResources(context.Background(), models.Config{
		InternalDomain:  "internal.example",
		ExternalDomain:  "external.example",
		InternalGateway: "istio-system/internal",
		ExternalGateway: "istio-system/external",
	}, ctxData, "prod", nil)
	if err != nil {
		t.Fatalf("unexpected applyDeployResources error: %v", err)
	}
}

func TestReconcileStrategyResourcesCanaryWithRenderedResources(t *testing.T) {
	namespace := "releasea-apps-production"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	renderedResources := []map[string]interface{}{
		map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "api",
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{},
					"spec": map[string]interface{}{
						"containers": []interface{}{map[string]interface{}{"name": "api"}},
					},
				},
			},
		},
		map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      "api",
				"namespace": namespace,
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{"app": "api"},
			},
		},
	}

	err := reconcileStrategyResources(
		context.Background(),
		models.Config{},
		models.DeployContext{
			Service: models.ServiceConfig{
				ID:   "svc-1",
				Name: "api",
				DeploymentStrategy: models.DeploymentStrategyConfig{
					Type:          "canary",
					CanaryPercent: 20,
				},
			},
		},
		"prod",
		"api",
		renderedResources,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected reconcile canary error: %v", err)
	}
}

func TestHandlePromoteCanaryWithKubeSimulation(t *testing.T) {
	namespace := "releasea-apps-production"
	var serviceCreated bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodPost && path == "/workers/credentials":
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:   "svc-1",
					Name: "api",
					DeploymentStrategy: models.DeploymentStrategyConfig{
						Type:          "canary",
						CanaryPercent: 20,
					},
				},
			})
		case r.Method == http.MethodGet && path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api-canary":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":      "api-canary",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{},
						"spec": map[string]interface{}{
							"containers": []interface{}{map[string]interface{}{"name": "api"}},
						},
					},
				},
			})
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace+"/services/api-canary":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api-canary",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-canary"},
				},
			})
		case r.Method == http.MethodGet && path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api":
			if !serviceCreated {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
					Replicas:          1,
				},
			})
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
		case r.Method == http.MethodPost && strings.Contains(path, "/deployments"):
			serviceCreated = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && strings.Contains(path, "/services"):
			serviceCreated = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.Contains(path, "/services/api"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
		case r.Method == http.MethodGet && path == "/apis/networking.istio.io/v1beta1/namespaces/"+namespace+"/virtualservices":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	op := models.OperationPayload{
		Resource: "svc-1",
		Payload:  map[string]interface{}{"environment": "prod"},
	}
	err := HandlePromoteCanary(context.Background(), server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("access-token"), op)
	if err != nil {
		t.Fatalf("unexpected handle promote canary error: %v", err)
	}
}
