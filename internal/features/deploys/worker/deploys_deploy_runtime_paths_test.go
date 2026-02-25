package deploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"releaseaworker/internal/platform/models"
	"strings"
	"testing"
)

func createLocalGitRepoForDeployTests(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v output=%s", args, err, string(out))
		}
	}

	run("init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test"), 0o644); err != nil {
		t.Fatalf("failed to write readme: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "public"), 0o755); err != nil {
		t.Fatalf("failed to create output dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "public", "index.html"), []byte("<html>ok</html>"), 0o644); err != nil {
		t.Fatalf("failed to write output file: %v", err)
	}
	run("add", ".")
	run("commit", "-m", "init")
	return dir
}

func TestWaitForServiceDeployReadinessEarlyReturn(t *testing.T) {
	err := waitForServiceDeployReadiness(
		context.Background(),
		models.Config{},
		"prod",
		"",
		"",
		nil,
		models.ServiceConfig{Type: "static-site"},
		nil,
	)
	if err != nil {
		t.Fatalf("expected early return without error, got %v", err)
	}
}

func TestResolveCanaryReadinessTargetsEarlyReturns(t *testing.T) {
	if got := resolveCanaryReadinessTargets(context.Background(), models.Config{}, "apps", "", []string{"api"}, nil); len(got) != 1 {
		t.Fatalf("expected unchanged targets when service name empty")
	}
	if got := resolveCanaryReadinessTargets(context.Background(), models.Config{}, "apps", "api", nil, nil); got != nil {
		t.Fatalf("expected unchanged nil targets")
	}
	if got := resolveCanaryReadinessTargets(context.Background(), models.Config{}, "apps", "api", []string{"api-canary"}, nil); len(got) != 1 {
		t.Fatalf("expected unchanged targets without stable alias")
	}
}

func TestCleanupStrategyShadowsBestEffortNoop(t *testing.T) {
	cleanupStrategyShadowsBestEffort(context.Background(), models.Config{}, "prod", "", "rolling", nil)
}

func TestApplyDeployResourcesEarlyValidation(t *testing.T) {
	err := applyDeployResources(context.Background(), models.Config{}, models.DeployContext{}, "prod", nil)
	if err == nil {
		t.Fatalf("expected deploy template missing error")
	}

	err = applyDeployResources(
		context.Background(),
		models.Config{},
		models.DeployContext{
			Template: &models.DeployTemplate{
				Resources: []map[string]interface{}{{"kind": "Deployment"}},
			},
		},
		"prod",
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "docker image missing") {
		t.Fatalf("expected docker image validation error, got %v", err)
	}
}

func TestApplyRenderedResourcesEarlyValidation(t *testing.T) {
	err := applyRenderedResources(context.Background(), models.Config{}, nil, "prod", models.DeployContext{}, nil)
	if err == nil {
		t.Fatalf("expected deploy resources missing error")
	}
}

func TestReconcileStrategyResourcesRollingNoop(t *testing.T) {
	err := reconcileStrategyResources(
		context.Background(),
		models.Config{},
		models.DeployContext{
			Service: models.ServiceConfig{
				DeploymentStrategy: models.DeploymentStrategyConfig{Type: "rolling"},
			},
		},
		"prod",
		"api",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("expected rolling reconcile noop, got %v", err)
	}
}

func TestBuildAndPushImageFullValidationAndRuntimeError(t *testing.T) {
	err := buildAndPushImageFull(context.Background(), models.Config{}, nil, nil, nil, "prod", "", nil)
	if err == nil {
		t.Fatalf("expected missing deploy context error")
	}

	repo := createLocalGitRepoForDeployTests(t)
	ctxData := &models.DeployContext{
		Service: models.ServiceConfig{
			ID:            "svc-1",
			Name:          "api",
			RepoURL:       repo,
			DockerImage:   "example/releasea-worker:test",
			DockerContext: ".",
		},
	}

	err = buildAndPushImageFull(
		context.Background(),
		models.Config{},
		ctxData,
		nil,
		nil,
		"prod",
		"",
		nil,
	)
	if err == nil {
		t.Fatalf("expected runtime error from docker build/push path")
	}
}

func TestHandleStaticSiteDeployRuntimeError(t *testing.T) {
	repo := createLocalGitRepoForDeployTests(t)
	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			ID:        "svc-1",
			Name:      "site",
			RepoURL:   repo,
			OutputDir: "public",
		},
	}

	err := handleStaticSiteDeploy(
		context.Background(),
		models.Config{
			MinioEndpoint:  "minio.local:9000",
			MinioAccessKey: "access",
			MinioSecretKey: "secret",
			MinioBucket:    "bucket",
		},
		ctxData,
		"prod",
		nil,
	)
	if err == nil {
		t.Fatalf("expected runtime error from mc command path")
	}
}
