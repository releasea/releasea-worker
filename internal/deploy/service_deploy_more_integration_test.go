package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"
	"testing"
)

func TestHandleServiceDeployGitWithResourcesYAMLRolling(t *testing.T) {
	commandLog := setupFakeCommands(t, map[string]string{
		"git": `
if [ -n "${FAKE_CMD_LOG:-}" ]; then
  echo "git $*" >> "$FAKE_CMD_LOG"
fi
cmd="${1:-}"
if [ "$cmd" = "clone" ]; then
  last=""
  for arg in "$@"; do
    last="$arg"
  done
  mkdir -p "$last"
  if [ -n "${FAKE_GIT_CREATE_PATH:-}" ]; then
    mkdir -p "$last/${FAKE_GIT_CREATE_PATH}"
  fi
  exit 0
fi
if [ "$cmd" = "rev-parse" ]; then
  echo "${FAKE_GIT_SHA:-0123456789abcdef0123456789abcdef01234567}"
  exit 0
fi
exit 0
`,
		"docker": `
if [ -n "${FAKE_CMD_LOG:-}" ]; then
  echo "docker $*" >> "$FAKE_CMD_LOG"
fi
cmd="${1:-}"
if [ "$cmd" = "inspect" ]; then
  echo "registry.example.com/releasea/api@sha256:abc123"
  exit 0
fi
if [ "$cmd" = "login" ]; then
  cat >/dev/null
  exit 0
fi
exit 0
`,
		"kubectl": `
if [ -n "${FAKE_CMD_LOG:-}" ]; then
  echo "kubectl $*" >> "$FAKE_CMD_LOG"
fi
cmd="${1:-}"
if [ "$cmd" = "apply" ]; then
  cat >/dev/null
  echo "applied"
  exit 0
fi
if [ "$cmd" = "patch" ]; then
  echo "patched"
  exit 0
fi
exit 0
`,
	})
	t.Setenv("FAKE_GIT_CREATE_PATH", "app")
	t.Setenv("FAKE_GIT_SHA", "0123456789abcdef0123456789abcdef01234567")

	namespace := "releasea-apps-production"
	buildRegistrations := 0
	deleteCalls := 0
	logUpdates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workers/credentials":
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:          "svc-git",
					Name:        "api",
					Type:        "microservice",
					SourceType:  "git",
					RepoURL:     "https://github.com/releasea/worker.git",
					Branch:      "main",
					RootDir:     "app",
					DockerImage: "registry.example.com/releasea/api:dev",
					DeploymentStrategy: models.DeploymentStrategyConfig{
						Type: "rolling",
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/workers/builds":
			buildRegistrations++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/deploys/dep-git/logs":
			logUpdates++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api":
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
					Replicas:          1,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
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
		case r.Method == http.MethodGet && r.URL.Path == "/apis/networking.istio.io/v1beta1/namespaces/"+namespace+"/virtualservices":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	op := models.OperationPayload{
		Resource: "svc-git",
		DeployID: "dep-git",
		Payload: map[string]interface{}{
			"environment":   "prod",
			"version":       "abc123",
			"image":         "registry.example.com/releasea/api:override",
			"resourcesYaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n",
		},
	}
	cfg := models.Config{ApiBaseURL: server.URL}
	err := HandleServiceDeploy(context.Background(), server.Client(), cfg, platform.NewTokenManager("access-token"), op)
	if err != nil {
		t.Fatalf("unexpected HandleServiceDeploy git/yaml error: %v", err)
	}
	if buildRegistrations != 1 {
		t.Fatalf("expected build registration call, got %d", buildRegistrations)
	}
	if logUpdates == 0 {
		t.Fatalf("expected deploy status/log updates")
	}
	if deleteCalls == 0 {
		t.Fatalf("expected shadow cleanup delete calls")
	}

	commands := readCommandLog(t, commandLog)
	if !strings.Contains(commands, "kubectl apply -f -") {
		t.Fatalf("expected kubectl apply command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "kubectl patch deployment api") {
		t.Fatalf("expected kubectl patch command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "git fetch --depth 1 origin abc123") {
		t.Fatalf("expected git fetch command, got:\n%s", commands)
	}
}

func TestHandleServiceDeployCanaryWithRenderedResources(t *testing.T) {
	namespace := "releasea-apps-production"
	deployments := map[string]bool{}
	services := map[string]bool{}
	logUpdates := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workers/credentials":
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:          "svc-canary",
					Name:        "api",
					Type:        "microservice",
					SourceType:  "registry",
					DockerImage: "registry.example.com/releasea/api:test",
					DeploymentStrategy: models.DeploymentStrategyConfig{
						Type:          "canary",
						CanaryPercent: 20,
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/deploys/dep-canary/logs":
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
					"name":      name,
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
				"status": map[string]interface{}{
					"availableReplicas": 1,
					"replicas":          1,
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			meta := body["metadata"].(map[string]interface{})
			name := strings.TrimSpace(meta["name"].(string))
			deployments[name] = true
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/apis/apps/v1/namespaces/"+namespace+"/deployments/"):
			name := strings.TrimPrefix(r.URL.Path, "/apis/apps/v1/namespaces/"+namespace+"/deployments/")
			deployments[name] = true
			w.WriteHeader(http.StatusOK)
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
					"name":      name,
					"namespace": namespace,
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
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/"+namespace+"/services/"):
			name := strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"+namespace+"/services/")
			services[name] = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)

	op := models.OperationPayload{
		Resource: "svc-canary",
		DeployID: "dep-canary",
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
								"containers": []interface{}{map[string]interface{}{"name": "api"}},
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
	cfg := models.Config{ApiBaseURL: server.URL}
	err := HandleServiceDeploy(context.Background(), server.Client(), cfg, platform.NewTokenManager("access-token"), op)
	if err != nil {
		t.Fatalf("unexpected HandleServiceDeploy canary error: %v", err)
	}
	if !deployments["api"] || !deployments["api-canary"] {
		t.Fatalf("expected stable and canary deployments, got %+v", deployments)
	}
	if !services["api"] || !services["api-canary"] {
		t.Fatalf("expected stable and canary services, got %+v", services)
	}
	if logUpdates == 0 {
		t.Fatalf("expected deploy status/log updates for canary flow")
	}
}
