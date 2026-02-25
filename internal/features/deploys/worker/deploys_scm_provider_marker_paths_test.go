package deploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGithubHasReleaseaMarkerPaths(t *testing.T) {
	repo := repoRef{Owner: "releasea", Name: "worker"}

	t.Run("empty token", func(t *testing.T) {
		ok, err := githubHasReleaseaMarker(context.Background(), &http.Client{}, "", repo)
		if err != nil {
			t.Fatalf("unexpected error for empty token: %v", err)
		}
		if ok {
			t.Fatalf("expected false when token is empty")
		}
	})

	t.Run("not found marker", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		repo.Host = parseHost(t, server.URL)
		ok, err := githubHasReleaseaMarker(context.Background(), server.Client(), "token", repo)
		if err != nil {
			t.Fatalf("unexpected 404 marker error: %v", err)
		}
		if ok {
			t.Fatalf("expected false for missing marker")
		}
	})

	t.Run("marker managed by another platform", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte(`{"managedBy":"custom-platform"}`)),
			})
		}))
		defer server.Close()

		repo.Host = parseHost(t, server.URL)
		ok, err := githubHasReleaseaMarker(context.Background(), server.Client(), "token", repo)
		if err != nil {
			t.Fatalf("unexpected marker decode error: %v", err)
		}
		if ok {
			t.Fatalf("expected false for non-releasea managed marker")
		}
	})

	t.Run("github api error body", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
		}))
		defer server.Close()

		repo.Host = parseHost(t, server.URL)
		_, err := githubHasReleaseaMarker(context.Background(), server.Client(), "token", repo)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "bad credentials") {
			t.Fatalf("expected github message in error, got %v", err)
		}
	})

	t.Run("invalid marker content", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte("not-json")),
			})
		}))
		defer server.Close()

		repo.Host = parseHost(t, server.URL)
		_, err := githubHasReleaseaMarker(context.Background(), server.Client(), "token", repo)
		if err == nil {
			t.Fatalf("expected json decode error for invalid marker content")
		}
	})
}

func TestDeleteManagedRepositoryUsesGithubMarker(t *testing.T) {
	deleteCalls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/v3/repos/releasea/worker/contents/.releasea/managed.json"):
			_ = json.NewEncoder(w).Encode(map[string]string{
				"encoding": "base64",
				"content":  base64.StdEncoding.EncodeToString([]byte(`{"managedBy":"releasea-platform"}`)),
			})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/api/v3/repos/releasea/worker"):
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := DeleteManagedRepository(context.Background(), kubeRewriteClient(server), DeleteInput{
		RepoURL:     "https://github.enterprise.local/releasea/worker",
		SourceType:  "git",
		Provider:    "github",
		Token:       "token",
		RepoManaged: false,
	})
	if err != nil {
		t.Fatalf("unexpected managed repository delete error: %v", err)
	}
	if deleteCalls != 1 {
		t.Fatalf("expected delete call when releasea marker exists, got %d", deleteCalls)
	}
}
