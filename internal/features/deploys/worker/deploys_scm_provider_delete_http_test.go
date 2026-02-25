package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func parseHost(t *testing.T, rawURL string) string {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse server url: %v", err)
	}
	return parsed.Host
}

func TestDeleteGithubRepo(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/api/v3/repos/releasea/worker") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	repo := repoRef{Host: parseHost(t, server.URL), Owner: "releasea", Name: "worker"}
	if err := deleteGithubRepo(context.Background(), server.Client(), "token", repo); err != nil {
		t.Fatalf("unexpected github delete error: %v", err)
	}
}

func TestDeleteGitlabRepo(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/api/v4/projects/releasea%2Fworker") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	repo := repoRef{Host: parseHost(t, server.URL), Path: "releasea/worker"}
	if err := deleteGitlabRepo(context.Background(), server.Client(), "token", repo); err != nil {
		t.Fatalf("unexpected gitlab delete error: %v", err)
	}
}

func TestDeleteBitbucketRepo(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/2.0/repositories/releasea/worker") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	repo := repoRef{Host: parseHost(t, server.URL), Owner: "releasea", Name: "worker"}
	if err := deleteBitbucketRepo(context.Background(), server.Client(), "token", repo); err != nil {
		t.Fatalf("unexpected bitbucket delete error: %v", err)
	}
}

func TestDeleteProviderErrors(t *testing.T) {
	t.Run("github error body", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "forbidden"})
		}))
		defer server.Close()

		repo := repoRef{Host: parseHost(t, server.URL), Owner: "releasea", Name: "worker"}
		err := deleteGithubRepo(context.Background(), server.Client(), "token", repo)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "forbidden") {
			t.Fatalf("expected github delete error with response message, got %v", err)
		}
	})

	t.Run("gitlab status error", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		repo := repoRef{Host: parseHost(t, server.URL), Path: "releasea/worker"}
		if err := deleteGitlabRepo(context.Background(), server.Client(), "token", repo); err == nil {
			t.Fatalf("expected gitlab status error")
		}
	})

	t.Run("bitbucket status error", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer server.Close()

		repo := repoRef{Host: parseHost(t, server.URL), Owner: "releasea", Name: "worker"}
		if err := deleteBitbucketRepo(context.Background(), server.Client(), "token", repo); err == nil {
			t.Fatalf("expected bitbucket status error")
		}
	})
}
