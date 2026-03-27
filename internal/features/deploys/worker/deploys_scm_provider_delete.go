package deploy

import (
	"context"
	"net/http"
	scmproviders "releaseaworker/internal/platform/providers/scm"
	"strings"
)

type repoRef = scmproviders.RepoRef
type githubContentResponse = scmproviders.GitHubContentResponse

type DeleteInput struct {
	RepoURL     string
	SourceType  string
	Provider    string
	Token       string
	RepoManaged bool
}

func DeleteManagedRepository(ctx context.Context, client *http.Client, input DeleteInput) error {
	repoURL := strings.TrimSpace(input.RepoURL)
	if repoURL == "" {
		return nil
	}
	if normalizeSourceType(input.SourceType) == "registry" {
		return nil
	}
	return scmproviders.DeleteManagedRepository(ctx, client, scmproviders.DeleteInput{
		RepoURL:     repoURL,
		Provider:    input.Provider,
		Token:       input.Token,
		RepoManaged: input.RepoManaged,
	})
}

func normalizeSourceType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "registry", "docker":
		return "registry"
	case "git":
		return "git"
	default:
		return ""
	}
}

func inferProvider(host string) string {
	return scmproviders.InferProvider(host)
}

func parseRepoRef(repoURL string) (repoRef, error) {
	return scmproviders.ParseRepoRef(repoURL)
}

func buildRepoRef(host, path string) (repoRef, error) {
	return scmproviders.BuildRepoRef(host, path)
}

func githubHasReleaseaMarker(ctx context.Context, client *http.Client, token string, repo repoRef) (bool, error) {
	return scmproviders.GitHubHasReleaseaMarker(ctx, client, token, repo)
}

func githubAPIBase(host string) string {
	return scmproviders.GitHubAPIBase(host)
}

func decodeGithubContent(content githubContentResponse) ([]byte, error) {
	return scmproviders.DecodeGitHubContent(content)
}

func githubErrorMessage(body []byte) string {
	return scmproviders.GitHubErrorMessage(body)
}

func escapeGithubPath(value string) string {
	return scmproviders.EscapeGitHubPath(value)
}

func deleteGithubRepo(ctx context.Context, client *http.Client, token string, repo repoRef) error {
	return scmproviders.DeleteGitHubRepo(ctx, client, token, repo)
}

func deleteGitlabRepo(ctx context.Context, client *http.Client, token string, repo repoRef) error {
	return scmproviders.DeleteGitLabRepo(ctx, client, token, repo)
}

func deleteBitbucketRepo(ctx context.Context, client *http.Client, token string, repo repoRef) error {
	return scmproviders.DeleteBitbucketRepo(ctx, client, token, repo)
}
