package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"
	"testing"
)

func TestHandleServiceDeleteStaticSiteAndManagedRepo(t *testing.T) {
	commandLog := setupFakeCommands(t, map[string]string{
		"mc": `
if [ -n "${FAKE_CMD_LOG:-}" ]; then
  echo "mc $*" >> "$FAKE_CMD_LOG"
fi
exit 0
`,
	})
	namespace := "releasea-apps-production"
	deleteCalls := 0
	markerChecks := 0
	repoDeletes := 0

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workers/credentials":
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:          "svc-1",
					Name:        "api",
					Type:        "static-site",
					SourceType:  "git",
					RepoURL:     "https://github.enterprise.local/releasea/worker",
					RepoManaged: true,
				},
				SCM: &models.SCMCredential{
					Provider: "github",
					Token:    "ghp_token",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/releasea/worker/contents/.releasea/managed.json":
			markerChecks++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte(`{"managedBy":"releasea-platform"}`)),
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v3/repos/releasea/worker":
			repoDeletes++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/namespaces/"+namespace+"/"):
			deleteCalls++
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	cfg := models.Config{
		ApiBaseURL:       server.URL,
		MinioEndpoint:    "minio.local:9000",
		MinioAccessKey:   "releasea",
		MinioSecretKey:   "releasea-secret",
		MinioBucket:      "releasea-static",
		StaticSitePrefix: "sites",
	}
	err := HandleServiceDelete(
		context.Background(),
		kubeRewriteClient(server),
		cfg,
		platform.NewTokenManager("access-token"),
		models.OperationPayload{
			Resource: "svc-1",
			Payload: map[string]interface{}{
				"environment": "prod",
			},
		},
	)
	if err != nil {
		t.Fatalf("unexpected HandleServiceDelete error: %v", err)
	}
	if deleteCalls < 10 {
		t.Fatalf("expected many kube delete calls, got %d", deleteCalls)
	}
	if markerChecks != 1 {
		t.Fatalf("expected marker check call, got %d", markerChecks)
	}
	if repoDeletes != 1 {
		t.Fatalf("expected managed repo delete call, got %d", repoDeletes)
	}

	commands := readCommandLog(t, commandLog)
	if !strings.Contains(commands, "mc alias set releasea-minio") {
		t.Fatalf("expected minio alias command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "mc rm --recursive --force releasea-minio/releasea-static/sites/api") {
		t.Fatalf("expected static site asset cleanup command, got:\n%s", commands)
	}
}

func TestPromoteBlueGreenSuccess(t *testing.T) {
	namespace := "releasea-apps-production"
	canonicalSelector := "api-blue"
	selectorUpdates := 0
	updateSlotCalls := 0
	deleteCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workers/services/svc-1/blue-green/primary":
			updateSlotCalls++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": canonicalSelector},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			selectorUpdates++
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			spec := body["spec"].(map[string]interface{})
			selector := spec["selector"].(map[string]interface{})
			canonicalSelector = selector["app"].(string)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api-green":
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
					Replicas:          1,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
		case r.Method == http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	t.Setenv("WORKER_BLUE_GREEN_OBSERVATION_SECONDS", "0")

	slot, err := promoteBlueGreen(
		context.Background(),
		server.Client(),
		models.Config{ApiBaseURL: server.URL},
		platform.NewTokenManager("access-token"),
		models.ServiceConfig{
			Name: "api",
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type:             "blue-green",
				BlueGreenPrimary: "blue",
			},
		},
		"svc-1",
		"prod",
		"api",
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected promoteBlueGreen error: %v", err)
	}
	if slot != "green" {
		t.Fatalf("expected promoted slot green, got %q", slot)
	}
	if updateSlotCalls != 1 {
		t.Fatalf("expected one API active-slot update call, got %d", updateSlotCalls)
	}
	if selectorUpdates == 0 || canonicalSelector != "api-green" {
		t.Fatalf("expected canonical service switched to api-green, selector=%q updates=%d", canonicalSelector, selectorUpdates)
	}
	if deleteCalls != 4 {
		t.Fatalf("expected 4 cleanup delete calls, got %d", deleteCalls)
	}
}

func TestPromoteBlueGreenRevertsOnSlotUpdateFailure(t *testing.T) {
	namespace := "releasea-apps-production"
	canonicalSelector := "api-blue"
	selectorUpdates := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workers/services/svc-1/blue-green/primary":
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": canonicalSelector},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			selectorUpdates++
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			spec := body["spec"].(map[string]interface{})
			selector := spec["selector"].(map[string]interface{})
			canonicalSelector = selector["app"].(string)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	t.Setenv("WORKER_BLUE_GREEN_OBSERVATION_SECONDS", "0")

	_, err := promoteBlueGreen(
		context.Background(),
		server.Client(),
		models.Config{ApiBaseURL: server.URL},
		platform.NewTokenManager("access-token"),
		models.ServiceConfig{
			Name: "api",
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type:             "blue-green",
				BlueGreenPrimary: "blue",
			},
		},
		"svc-1",
		"prod",
		"api",
		nil,
	)
	if err == nil {
		t.Fatalf("expected promoteBlueGreen failure when active slot API update fails")
	}
	if selectorUpdates < 2 {
		t.Fatalf("expected selector switch and rollback updates, got %d", selectorUpdates)
	}
	if canonicalSelector != "api-blue" {
		t.Fatalf("expected canonical selector rollback to api-blue, got %q", canonicalSelector)
	}
}

func TestReconcileStrategyResourcesCanaryRawYAMLPath(t *testing.T) {
	commandLog := setupFakeCommands(t, map[string]string{
		"kubectl": `
if [ -n "${FAKE_CMD_LOG:-}" ]; then
  echo "kubectl $*" >> "$FAKE_CMD_LOG"
fi
cmd="${1:-}"
if [ "$cmd" = "rollout" ]; then
  echo "rollout undone"
  exit 0
fi
if [ "$cmd" = "patch" ]; then
  echo "patched"
  exit 0
fi
exit 0
`,
	})
	namespace := "releasea-apps-production"
	canaryDeploymentCreated := false
	canaryServiceCreated := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
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
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api-canary":
			if !canaryDeploymentCreated {
				w.WriteHeader(http.StatusNotFound)
				return
			}
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api-canary":
			if !canaryServiceCreated {
				w.WriteHeader(http.StatusNotFound)
				return
			}
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
		case r.Method == http.MethodPost && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments":
			canaryDeploymentCreated = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services":
			canaryServiceCreated = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/apis/autoscaling/v2/namespaces/"+namespace+"/horizontalpodautoscalers/"):
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	err := reconcileStrategyResources(
		context.Background(),
		models.Config{},
		models.DeployContext{
			Service: models.ServiceConfig{
				ID:   "svc-1",
				Name: "api",
				DeploymentStrategy: models.DeploymentStrategyConfig{
					Type:          "canary",
					CanaryPercent: 10,
				},
			},
		},
		"prod",
		"api",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected reconcileStrategyResources error: %v", err)
	}
	if !canaryDeploymentCreated || !canaryServiceCreated {
		t.Fatalf("expected canary deployment and service creation, got deployment=%v service=%v", canaryDeploymentCreated, canaryServiceCreated)
	}

	commands := readCommandLog(t, commandLog)
	if !strings.Contains(commands, "kubectl rollout undo deployment/api -n "+namespace) {
		t.Fatalf("expected rollout undo command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "kubectl patch deployment api-canary -n "+namespace) {
		t.Fatalf("expected canary patch command, got:\n%s", commands)
	}
}
