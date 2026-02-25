package deploy

import (
	"context"
	"releaseaworker/internal/models"
	"testing"
)

func TestStaticSiteHelpers(t *testing.T) {
	if got := resolveStaticOutputDir(""); got != defaultStaticOutputDir {
		t.Fatalf("expected default output dir, got %q", got)
	}
	if got := resolveStaticOutputDir("build"); got != "build" {
		t.Fatalf("expected custom output dir build, got %q", got)
	}

	cfg := models.Config{StaticSitePrefix: "sites"}
	svc := models.ServiceConfig{Name: "My Site"}
	if got := staticSitePrefix(cfg, svc); got != "sites/my-site" {
		t.Fatalf("unexpected static site prefix: %q", got)
	}
	if got := staticSitePrefix(models.Config{}, models.ServiceConfig{}); got != "site" {
		t.Fatalf("expected fallback site prefix, got %q", got)
	}

	if got := parseCacheTTL(""); got != defaultStaticCacheTTL {
		t.Fatalf("expected default ttl, got %d", got)
	}
	if got := parseCacheTTL("-1"); got != 0 {
		t.Fatalf("expected ttl 0 for negative input, got %d", got)
	}
	if got := parseCacheTTL("120"); got != 120 {
		t.Fatalf("expected parsed ttl 120, got %d", got)
	}

	if got := cacheControlValue("0"); got != "no-cache" {
		t.Fatalf("expected no-cache for ttl 0, got %q", got)
	}
	if got := cacheControlValue("60"); got != "public, max-age=60" {
		t.Fatalf("unexpected cache control value: %q", got)
	}
	if got := cacheControlAttr(""); got != "" {
		t.Fatalf("expected empty attr for empty cache control, got %q", got)
	}
	if got := cacheControlAttr("public, max-age=60"); got == "" {
		t.Fatalf("expected cache control attr")
	}
}

func TestHandleStaticSiteDeployValidationErrors(t *testing.T) {
	err := handleStaticSiteDeploy(
		context.Background(),
		models.Config{},
		models.DeployContext{},
		"prod",
		nil,
	)
	if err == nil {
		t.Fatalf("expected repository validation error")
	}
}

func TestEnsureMinioAliasValidationErrors(t *testing.T) {
	_, err := ensureMinioAlias(context.Background(), models.Config{}, nil)
	if err == nil {
		t.Fatalf("expected minio endpoint validation error")
	}

	_, err = ensureMinioAlias(context.Background(), models.Config{MinioEndpoint: "minio.local:9000"}, nil)
	if err == nil {
		t.Fatalf("expected minio credentials validation error")
	}
}

func TestDeleteStaticSiteAssetsValidationError(t *testing.T) {
	err := deleteStaticSiteAssets(context.Background(), models.Config{}, models.ServiceConfig{Name: "site"})
	if err == nil {
		t.Fatalf("expected static asset delete validation error")
	}
}
