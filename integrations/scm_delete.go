package integrations

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

	commonsource "releaseaworker/common/source"
)

const (
	releaseaManagedBy         = "releasea-platform"
	releaseaManagedMarkerPath = ".releasea/managed.json"
)

type DeleteInput struct {
	RepoURL     string
	SourceType  string
	Provider    string
	Token       string
	RepoManaged bool
}

type repoRef struct {
	Host  string
	Path  string
	Owner string
	Name  string
}

type releaseaManagedMarker struct {
	ManagedBy string `json:"managedBy"`
}

type githubContentResponse struct {
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

type githubError struct {
	Message string `json:"message"`
}

func DeleteManagedRepository(ctx context.Context, client *http.Client, input DeleteInput) error {
	repoURL := strings.TrimSpace(input.RepoURL)
	if repoURL == "" {
		return nil
	}
	if commonsource.NormalizeType(input.SourceType) == "registry" {
		return nil
	}

	repo, err := parseRepoRef(repoURL)
	if err != nil {
		return nil
	}

	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if provider == "" {
		provider = inferProvider(repo.Host)
	}

	token := strings.TrimSpace(input.Token)
	managed := input.RepoManaged
	if provider == "github" {
		ok, err := githubHasReleaseaMarker(ctx, client, token, repo)
		if err != nil {
			return err
		}
		if ok {
			managed = true
		}
	}
	if !managed {
		return nil
	}
	if token == "" {
		return errors.New("SCM credential missing token for repository delete")
	}

	switch provider {
	case "github":
		return deleteGithubRepo(ctx, client, token, repo)
	case "gitlab":
		return deleteGitlabRepo(ctx, client, token, repo)
	case "bitbucket":
		return deleteBitbucketRepo(ctx, client, token, repo)
	default:
		return fmt.Errorf("SCM provider %s not supported for repository delete", provider)
	}
}

func inferProvider(host string) string {
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

func parseRepoRef(repoURL string) (repoRef, error) {
	trimmed := strings.TrimSpace(repoURL)
	if trimmed == "" {
		return repoRef{}, errors.New("repo url empty")
	}
	if strings.HasPrefix(trimmed, "git@") {
		parts := strings.SplitN(trimmed, ":", 2)
		if len(parts) != 2 {
			return repoRef{}, errors.New("invalid ssh repo url")
		}
		host := strings.TrimPrefix(parts[0], "git@")
		return buildRepoRef(host, parts[1])
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return repoRef{}, err
	}
	return buildRepoRef(parsed.Hostname(), parsed.Path)
}

func buildRepoRef(host, path string) (repoRef, error) {
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	segments := strings.Split(path, "/")
	if len(segments) < 2 {
		return repoRef{}, errors.New("invalid repo path")
	}
	owner := segments[0]
	name := segments[len(segments)-1]
	return repoRef{
		Host:  strings.ToLower(strings.TrimSpace(host)),
		Path:  strings.Join(segments, "/"),
		Owner: owner,
		Name:  name,
	}, nil
}

func githubHasReleaseaMarker(ctx context.Context, client *http.Client, token string, repo repoRef) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, nil
	}
	base := githubAPIBase(repo.Host)
	path := escapeGithubPath(releaseaManagedMarkerPath)
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
		msg := githubErrorMessage(body)
		if msg == "" {
			msg = resp.Status
		}
		return false, errors.New(msg)
	}
	var content githubContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&content); err != nil {
		return false, err
	}
	decoded, err := decodeGithubContent(content)
	if err != nil {
		return false, err
	}
	var marker releaseaManagedMarker
	if err := json.Unmarshal(decoded, &marker); err != nil {
		return false, err
	}
	return marker.ManagedBy == releaseaManagedBy, nil
}

func deleteGithubRepo(ctx context.Context, client *http.Client, token string, repo repoRef) error {
	base := githubAPIBase(repo.Host)
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
		msg := githubErrorMessage(body)
		if msg == "" {
			msg = resp.Status
		}
		return errors.New(msg)
	}
	return nil
}

func deleteGitlabRepo(ctx context.Context, client *http.Client, token string, repo repoRef) error {
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

func deleteBitbucketRepo(ctx context.Context, client *http.Client, token string, repo repoRef) error {
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

func githubAPIBase(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "github.com" {
		return "https://api.github.com"
	}
	return "https://" + host + "/api/v3"
}

func decodeGithubContent(content githubContentResponse) ([]byte, error) {
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

func githubErrorMessage(body []byte) string {
	var ghErr githubError
	if err := json.Unmarshal(body, &ghErr); err == nil {
		if msg := strings.TrimSpace(ghErr.Message); msg != "" {
			return msg
		}
	}
	return ""
}

func escapeGithubPath(value string) string {
	segments := strings.Split(value, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
