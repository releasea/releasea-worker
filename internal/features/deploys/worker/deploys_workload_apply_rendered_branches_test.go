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
)

func TestApplyRenderedResourcesCanaryFirstDeployAndSkips(t *testing.T) {
	namespace := "releasea-apps-production"
	deployments := map[string]bool{}
	services := map[string]bool{}
	configMaps := map[string]bool{}
	logUpdates := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/deploys/dep-render/logs":
			logUpdates++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/apis/apps/v1/namespaces/"+namespace+"/deployments/"):
			name := strings.TrimPrefix(r.URL.Path, "/apis/apps/v1/namespaces/"+namespace+"/deployments/")
			if !deployments[name] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata": map[string]interface{}{
					"name":            name,
					"namespace":       namespace,
					"resourceVersion": "1",
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
		case r.Method == http.MethodPost && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			meta := body["metadata"].(map[string]interface{})
			name := strings.TrimSpace(meta["name"].(string))
			deployments[name] = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/"+namespace+"/services/"):
			name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"+namespace+"/services/")
			if !services[name] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":            name,
					"namespace":       namespace,
					"resourceVersion": "1",
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": name},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			meta := body["metadata"].(map[string]interface{})
			name := strings.TrimSpace(meta["name"].(string))
			services[name] = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/"+namespace+"/configmaps/"):
			name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"+namespace+"/configmaps/")
			if !configMaps[name] {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "ConfigMap",
				"metadata": map[string]interface{}{
					"name":            name,
					"namespace":       namespace,
					"resourceVersion": "1",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/"+namespace+"/configmaps":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			meta := body["metadata"].(map[string]interface{})
			name := strings.TrimSpace(meta["name"].(string))
			configMaps[name] = true
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	logger := platform.NewDeployLogger(server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("access-token"), "dep-render")
	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			ID:          "svc-1",
			Name:        "api",
			DockerImage: "registry.example.com/releasea/api:test",
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type:          "canary",
				CanaryPercent: 20,
			},
		},
	}
	resources := []map[string]interface{}{
		{
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
		{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": "api",
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{"app": "api"},
			},
		},
		{
			"apiVersion": "networking.istio.io/v1beta1",
			"kind":       "VirtualService",
			"metadata": map[string]interface{}{
				"name": "api-route",
			},
		},
		{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name": "ignored-cluster-scope",
			},
		},
		{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "api-config",
			},
			"data": map[string]interface{}{"MODE": "prod"},
		},
	}
	if err := applyRenderedResources(context.Background(), models.Config{ApiBaseURL: server.URL}, resources, "prod", ctxData, logger); err != nil {
		t.Fatalf("unexpected applyRenderedResources canary error: %v", err)
	}

	if !deployments["api"] || !deployments["api-canary"] {
		t.Fatalf("expected stable and canary deployments, got %+v", deployments)
	}
	if !services["api"] || !services["api-canary"] {
		t.Fatalf("expected stable and canary services, got %+v", services)
	}
	if !configMaps["api-config"] {
		t.Fatalf("expected configmap apply")
	}
	if logUpdates == 0 {
		t.Fatalf("expected render/apply log updates")
	}
}

func TestApplyRenderedResourcesBlueGreenSkipsStableResources(t *testing.T) {
	namespace := "releasea-apps-production"
	deployApplyCount := 0
	serviceApplyCount := 0
	configMapApplyCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/namespaces/"+namespace:
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments":
			deployApplyCount++
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/services/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services":
			serviceApplyCount++
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/configmaps/"):
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/"+namespace+"/configmaps":
			configMapApplyCount++
			w.WriteHeader(http.StatusCreated)
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
			DockerImage: "registry.example.com/releasea/api:test",
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type:             "blue-green",
				BlueGreenPrimary: "blue",
			},
		},
	}
	resources := []map[string]interface{}{
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "api",
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
		{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": "api",
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{"app": "api"},
			},
		},
		{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata": map[string]interface{}{
				"name": "api-config",
			},
			"data": map[string]interface{}{"MODE": "prod"},
		},
	}
	if err := applyRenderedResources(context.Background(), models.Config{}, resources, "prod", ctxData, nil); err != nil {
		t.Fatalf("unexpected applyRenderedResources blue-green error: %v", err)
	}
	if deployApplyCount != 0 || serviceApplyCount != 0 {
		t.Fatalf("expected blue-green managed deployment/service to be skipped, got deployment=%d service=%d", deployApplyCount, serviceApplyCount)
	}
	if configMapApplyCount != 1 {
		t.Fatalf("expected non-managed configmap to be applied once, got %d", configMapApplyCount)
	}
}

func TestApplyRenderedResourcesValidationAndKubeClientErrors(t *testing.T) {
	resources := []map[string]interface{}{
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "api",
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
	}
	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			ID:          "svc-1",
			Name:        "api",
			DockerImage: "registry.example.com/releasea/api:test",
		},
	}

	t.Run("kube client error", func(t *testing.T) {
		t.Setenv("RELEASEA_KUBE_TOKEN_FILE", "/tmp/not-found-token")
		err := applyRenderedResources(context.Background(), models.Config{}, resources, "prod", ctxData, nil)
		if err == nil {
			t.Fatalf("expected kube client initialization error")
		}
	})
}
