package scmproviders

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	releaseaManagedBy         = "releasea-platform"
	releaseaManagedMarkerPath = ".releasea/managed.json"
)

var ErrMissingDeleteToken = errors.New("SCM credential missing token for repository delete")

type DeleteInput struct {
	RepoURL     string
	Provider    string
	Token       string
	RepoManaged bool
}

type RepoRef struct {
	Host  string
	Path  string
	Owner string
	Name  string
}

type GitHubContentResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type releaseaManagedMarker struct {
	ManagedBy string `json:"managedBy"`
}

type gitHubError struct {
	Message string `json:"message"`
}

func DeleteManagedRepository(ctx context.Context, client *http.Client, input DeleteInput) error {
	repoURL := strings.TrimSpace(input.RepoURL)
	if repoURL == "" {
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	repo, err := ParseRepoRef(repoURL)
	if err != nil {
		return nil
	}
	if provider == "" {
		provider = InferProvider(repo.Host)
	}
	if provider == "" && !input.RepoManaged {
		return nil
	}

	runtime, ok := ResolveRuntime(provider)
	if !ok {
		if input.RepoManaged {
			return ErrUnsupportedDeleteProvider(provider)
		}
		return nil
	}
	return runtime.DeleteManagedRepository(ctx, client, strings.TrimSpace(input.Token), repoURL, input.RepoManaged)
}

func InferProvider(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case strings.Contains(host, "github"):
		return "github"
	case strings.Contains(host, "gitlab"):
		return "gitlab"
	case strings.Contains(host, "bitbucket"):
		return "bitbucket"
	default:
		return ""
	}
}

func ParseRepoRef(repoURL string) (RepoRef, error) {
	trimmed := strings.TrimSpace(repoURL)
	if trimmed == "" {
		return RepoRef{}, errors.New("repo url empty")
	}
	if strings.HasPrefix(trimmed, "git@") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return RepoRef{}, errors.New("invalid ssh repo url")
		}
		host := strings.TrimPrefix(parts[0], "git@")
		return BuildRepoRef(host, parts[1])
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return RepoRef{}, err
	}
	return BuildRepoRef(parsed.Hostname(), parsed.Path)
}

func BuildRepoRef(host, path string) (RepoRef, error) {
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return RepoRef{}, errors.New("invalid repo path")
	}
	return RepoRef{
		Host:  strings.ToLower(strings.TrimSpace(host)),
		Path:  strings.Join(segments, "/"),
		Owner: segments[0],
		Name:  segments[len(segments)-1],
	}, nil
}

func GitHubHasReleaseaMarker(ctx context.Context, client *http.Client, token string, repo RepoRef) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	base := GitHubAPIBase(repo.Host)
	path := EscapeGitHubPath(releaseaManagedMarkerPath)
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s", base, repo.Owner, repo.Name, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "releasea-worker")
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		msg := GitHubErrorMessage(body)
		if msg == "" {
			msg = resp.Status
		}
		return false, errors.New(msg)
	}
	var content GitHubContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&content); err != nil {
		return false, err
	}
	decoded, err := DecodeGitHubContent(content)
	if err != nil {
		return false, err
	}
	var marker releaseaManagedMarker
	if err := json.Unmarshal(decoded, &marker); err != nil {
		return false, err
	}
	return marker.ManagedBy == releaseaManagedBy, nil
}

func ErrUnsupportedDeleteProvider(provider string) error {
	return fmt.Errorf("SCM provider %s not supported for repository delete", strings.TrimSpace(provider))
}

func GitHubAPIBase(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

func DecodeGitHubContent(content GitHubContentResponse) ([]byte, error) {
	if strings.ToLower(content.Encoding) != "base64" {
		return nil, errors.New("unexpected github content encoding")
	}
	decoded, err := base64.StdEncoding.DecodeString(content.Content)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
		if err != nil {
			return nil, err
		}
	}
	return decoded, nil
}

func GitHubErrorMessage(body []byte) string {
	var ghErr gitHubError
	if err := json.Unmarshal(body, &ghErr); err == nil {
		if msg := strings.TrimSpace(ghErr.Message); msg != "" {
			return msg
		}
	}
	return ""
}

func EscapeGitHubPath(value string) string {
	segments := strings.Split(value, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func DeleteGitHubRepo(ctx context.Context, client *http.Client, token string, repo RepoRef) error {
	base := GitHubAPIBase(repo.Host)
	deleteURL := fmt.Sprintf("%s/repos/%s/%s", base, repo.Owner, repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "releasea-worker")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusAccepted {
		return nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		msg := GitHubErrorMessage(body)
		if msg == "" {
			msg = resp.Status
		}
		return errors.New(msg)
	}
	return nil
}

func DeleteGitLabRepo(ctx context.Context, client *http.Client, token string, repo RepoRef) error {
	host := repo.Host
	if host == "" {
		host = "gitlab.com"
	}
	base := "https://" + host
	deleteURL := fmt.Sprintf("%s/api/v4/projects/%s", base, url.PathEscape(repo.Path))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("gitlab delete failed: %s", resp.Status)
	}
	return nil
}

func DeleteBitbucketRepo(ctx context.Context, client *http.Client, token string, repo RepoRef) error {
	host := repo.Host
	if host == "" {
		host = "bitbucket.org"
	}
	base := "https://api.bitbucket.org/2.0"
	if host != "bitbucket.org" {
		base = "https://" + host + "/2.0"
	}
	deleteURL := fmt.Sprintf("%s/repositories/%s/%s", base, repo.Owner, repo.Name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, deleteURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("bitbucket delete failed: %s", resp.Status)
	}
	return nil
}
