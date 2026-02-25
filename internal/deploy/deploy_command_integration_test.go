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

func writeExecutable(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write executable %s: %v", name, err)
	}
}

func setupFakeCommands(t *testing.T, scripts map[string]string) string {
	t.Helper()
	binDir := t.TempDir()
	for name, body := range scripts {
		writeExecutable(t, binDir, name, body)
	}
	commandLog := filepath.Join(t.TempDir(), "commands.log")
	t.Setenv("FAKE_CMD_LOG", commandLog)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	return commandLog
}

func readCommandLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read command log: %v", err)
	}
	return string(data)
}

func TestBuildAndPushImageFullSuccess(t *testing.T) {
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
if [ "$cmd" = "login" ]; then
  cat >/dev/null
  exit 0
fi
if [ "$cmd" = "inspect" ]; then
  echo "${FAKE_DOCKER_INSPECT:-registry.example.com/releasea/api@sha256:abc123}"
  exit 0
fi
exit 0
`,
	})
	t.Setenv("FAKE_GIT_CREATE_PATH", "app")
	t.Setenv("FAKE_GIT_SHA", "0123456789abcdef0123456789abcdef01234567")
	t.Setenv("FAKE_DOCKER_INSPECT", "registry.example.com/releasea/api@sha256:abc123")
	t.Setenv("HTTP_PROXY", "http://proxy.internal:3128")
	t.Setenv("DOCKER_BUILD_NETWORK", "host")

	buildRegistrations := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/workers/builds" {
			buildRegistrations++
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()

	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			ID:               "svc-1",
			Name:             "api",
			SourceType:       "git",
			RepoURL:          "https://github.com/releasea/worker.git",
			Branch:           "main",
			RootDir:          "app",
			DockerImage:      "registry.example.com/releasea/api:dev",
			PreDeployCommand: "echo predeploy",
		},
		SCM: &models.SCMCredential{
			Provider: "github",
			Token:    "ghp_token",
		},
		Registry: &models.RegistryCredential{
			ID:          "reg-1",
			RegistryUrl: "registry.example.com",
			Username:    "bot",
			Password:    "secret",
		},
	}

	err := buildAndPushImageFull(
		context.Background(),
		models.Config{ApiBaseURL: api.URL},
		&ctxData,
		api.Client(),
		platform.NewTokenManager("access-token"),
		"prod",
		"abc123",
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected buildAndPushImageFull error: %v", err)
	}
	if buildRegistrations != 1 {
		t.Fatalf("expected one build registration, got %d", buildRegistrations)
	}
	if ctxData.Service.DockerImage != "registry.example.com/releasea/api:g01234567" {
		t.Fatalf("expected docker image pinned to git tag, got %q", ctxData.Service.DockerImage)
	}

	commands := readCommandLog(t, commandLog)
	if !strings.Contains(commands, "git fetch --depth 1 origin abc123") {
		t.Fatalf("expected fetch command in log, got:\n%s", commands)
	}
	if !strings.Contains(commands, "git checkout abc123") {
		t.Fatalf("expected checkout command in log, got:\n%s", commands)
	}
	if !strings.Contains(commands, "docker login registry.example.com -u bot --password-stdin") {
		t.Fatalf("expected docker login in log, got:\n%s", commands)
	}
	if !strings.Contains(commands, "docker push registry.example.com/releasea/api:g01234567") {
		t.Fatalf("expected git-tag push in log, got:\n%s", commands)
	}
}

func TestHandleServiceDeployStaticSiteSuccess(t *testing.T) {
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
exit 0
`,
		"mc": `
if [ -n "${FAKE_CMD_LOG:-}" ]; then
  echo "mc $*" >> "$FAKE_CMD_LOG"
fi
if [ "${1:-}" = "anonymous" ] && [ "${FAKE_MC_FAIL_ANONYMOUS:-0}" = "1" ]; then
  echo "anonymous failed" >&2
  exit 1
fi
exit 0
`,
	})
	t.Setenv("FAKE_GIT_CREATE_PATH", "site/dist")
	t.Setenv("FAKE_MC_FAIL_ANONYMOUS", "1")

	logUpdates := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/workers/credentials":
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:             "svc-static",
					Name:           "landing",
					Type:           "static-site",
					SourceType:     "git",
					RepoURL:        "https://github.com/releasea/landing.git",
					Branch:         "main",
					RootDir:        "site",
					OutputDir:      "dist",
					InstallCommand: "echo install",
					BuildCommand:   "echo build",
					CacheTTL:       "120",
				},
				SCM: &models.SCMCredential{
					Provider: "github",
					Token:    "ghp_token",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/deploys/dep-static/logs":
			logUpdates++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	cfg := models.Config{
		ApiBaseURL:      api.URL,
		MinioEndpoint:   "minio.local:9000",
		MinioAccessKey:  "releasea",
		MinioSecretKey:  "releasea-secret",
		MinioBucket:     "releasea-static",
		StaticSitePrefix: "sites",
	}
	op := models.OperationPayload{
		Resource: "svc-static",
		DeployID: "dep-static",
		Payload: map[string]interface{}{
			"environment": "prod",
		},
	}
	if err := HandleServiceDeploy(context.Background(), api.Client(), cfg, platform.NewTokenManager("access-token"), op); err != nil {
		t.Fatalf("unexpected static site deploy error: %v", err)
	}
	if logUpdates == 0 {
		t.Fatalf("expected deploy strategy/log updates")
	}

	commands := readCommandLog(t, commandLog)
	if !strings.Contains(commands, "mc anonymous set download releasea-minio/releasea-static") {
		t.Fatalf("expected anonymous policy command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "mc policy set download releasea-minio/releasea-static") {
		t.Fatalf("expected fallback policy command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "mc mirror --overwrite --remove --attr Cache-Control=public, max-age=120") {
		t.Fatalf("expected mirror command with cache-control, got:\n%s", commands)
	}
}

func TestKubectlHelpersSuccessPaths(t *testing.T) {
	commandLog := setupFakeCommands(t, map[string]string{
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
if [ "$cmd" = "rollout" ]; then
  echo "rolled back"
  exit 0
fi
exit 0
`,
	})

	if err := applyResourcesYAML(context.Background(), "kind: Deployment\nmetadata:\n  name: api", nil); err != nil {
		t.Fatalf("unexpected applyResourcesYAML error: %v", err)
	}
	stampDeployRevisionKubectl(context.Background(), models.Config{}, "prod", "api", models.ServiceConfig{}, nil)
	stampDeployRevisionKubectl(context.Background(), models.Config{}, "prod", "cron-worker", models.ServiceConfig{
		Type: "cronjob",
	}, nil)

	commands := readCommandLog(t, commandLog)
	if !strings.Contains(commands, "kubectl apply -f -") {
		t.Fatalf("expected kubectl apply command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "kubectl patch deployment api") {
		t.Fatalf("expected deployment patch command, got:\n%s", commands)
	}
	if !strings.Contains(commands, "kubectl patch cronjob cron-worker") {
		t.Fatalf("expected cronjob patch command, got:\n%s", commands)
	}
}

func TestResolveCanaryReadinessTargetsUsesCanonicalSelector(t *testing.T) {
	namespace := "releasea-apps-production"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-blue"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api-blue":
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
					Replicas:          1,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	targets := resolveCanaryReadinessTargets(
		context.Background(),
		models.Config{},
		namespace,
		"api",
		[]string{"api", "api-canary"},
		nil,
	)
	if len(targets) != 2 {
		t.Fatalf("expected 2 readiness targets, got %v", targets)
	}
	if targets[0] != "api-blue" || targets[1] != "api-canary" {
		t.Fatalf("expected canonical target replacement, got %v", targets)
	}
}

func TestWaitForServiceDeployReadinessFailsOnDeploymentCondition(t *testing.T) {
	namespace := "releasea-apps-production"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Progressing",
							"status": "False",
							"reason": "ImagePullBackOff",
						},
					},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	err := waitForServiceDeployReadiness(
		context.Background(),
		models.Config{},
		"prod",
		namespace,
		"api",
		[]string{"api"},
		models.ServiceConfig{},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("expected deployment failure reason, got %v", err)
	}
}
