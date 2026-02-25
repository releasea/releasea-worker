package deploy

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeSourceType(t *testing.T) {
	if got := normalizeSourceType("docker"); got != "registry" {
		t.Fatalf("expected registry, got %q", got)
	}
	if got := normalizeSourceType("git"); got != "git" {
		t.Fatalf("expected git, got %q", got)
	}
	if got := normalizeSourceType("unknown"); got != "" {
		t.Fatalf("expected empty fallback, got %q", got)
	}
}

func TestInferProvider(t *testing.T) {
	if got := inferProvider("github.enterprise.local"); got != "github" {
		t.Fatalf("expected github, got %q", got)
	}
	if got := inferProvider("gitlab.com"); got != "gitlab" {
		t.Fatalf("expected gitlab, got %q", got)
	}
	if got := inferProvider("bitbucket.org"); got != "bitbucket" {
		t.Fatalf("expected bitbucket, got %q", got)
	}
	if got := inferProvider("example.com"); got != "" {
		t.Fatalf("expected empty provider, got %q", got)
	}
}

func TestParseRepoRef(t *testing.T) {
	repo, err := parseRepoRef("https://github.com/releasea/worker.git")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if repo.Owner != "releasea" || repo.Name != "worker" || repo.Host != "github.com" {
		t.Fatalf("unexpected repo ref: %+v", repo)
	}

	repo, err = parseRepoRef("git@github.com:releasea/worker.git")
	if err != nil {
		t.Fatalf("unexpected ssh parse error: %v", err)
	}
	if repo.Path != "releasea/worker" {
		t.Fatalf("unexpected ssh path: %q", repo.Path)
	}

	if _, err := parseRepoRef("invalid-url"); err == nil {
		t.Fatalf("expected error for invalid repo path")
	}
}

func TestBuildRepoRef(t *testing.T) {
	repo, err := buildRepoRef("gitlab.com", "/group/sub/repo.git")
	if err != nil {
		t.Fatalf("unexpected build error: %v", err)
	}
	if repo.Path != "group/sub/repo" || repo.Owner != "group" || repo.Name != "repo" {
		t.Fatalf("unexpected built repo: %+v", repo)
	}

	if _, err := buildRepoRef("github.com", "/single-segment"); err == nil {
		t.Fatalf("expected invalid path error")
	}
}

func TestGitHubHelpers(t *testing.T) {
	if got := githubAPIBase("github.com"); got != "https://api.github.com" {
		t.Fatalf("unexpected github api base: %q", got)
	}
	if got := githubAPIBase("github.enterprise.local"); got != "https://github.enterprise.local/api/v3" {
		t.Fatalf("unexpected enterprise github api base: %q", got)
	}

	content := githubContentResponse{
		Encoding: "base64",
		Content:  base64.StdEncoding.EncodeToString([]byte(`{"managedBy":"releasea-platform"}`)),
	}
	decoded, err := decodeGithubContent(content)
	if err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if string(decoded) == "" {
		t.Fatalf("expected decoded content")
	}

	if _, err := decodeGithubContent(githubContentResponse{Encoding: "plain"}); err == nil {
		t.Fatalf("expected invalid encoding error")
	}

	msg := githubErrorMessage([]byte(`{"message":"bad credentials"}`))
	if msg != "bad credentials" {
		t.Fatalf("unexpected github error message: %q", msg)
	}

	escaped := escapeGithubPath(".releasea/managed.json")
	if escaped != ".releasea/managed.json" {
		t.Fatalf("unexpected escaped path: %q", escaped)
	}
}

func TestDeleteManagedRepositoryValidationPaths(t *testing.T) {
	ctx := context.Background()
	client := &http.Client{}

	err := DeleteManagedRepository(ctx, client, DeleteInput{
		RepoURL:    "",
		SourceType: "git",
	})
	if err != nil {
		t.Fatalf("empty repo url should be noop, got %v", err)
	}

	err = DeleteManagedRepository(ctx, client, DeleteInput{
		RepoURL:    "docker.io/releasea/worker",
		SourceType: "registry",
	})
	if err != nil {
		t.Fatalf("registry source should be noop, got %v", err)
	}

	err = DeleteManagedRepository(ctx, client, DeleteInput{
		RepoURL:     "https://github.com/releasea/worker",
		SourceType:  "git",
		Provider:    "github",
		RepoManaged: true,
		Token:       "",
	})
	if err == nil || !strings.Contains(err.Error(), "missing token") {
		t.Fatalf("expected missing token error, got %v", err)
	}

	err = DeleteManagedRepository(ctx, client, DeleteInput{
		RepoURL:     "https://example.com/releasea/worker",
		SourceType:  "git",
		Provider:    "unknown",
		RepoManaged: true,
		Token:       "abc",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "not supported") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}
