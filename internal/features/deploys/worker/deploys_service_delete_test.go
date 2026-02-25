package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/platform/models"
	"strings"
	"testing"
)

func TestFetchServiceContext(t *testing.T) {
	t.Run("missing service id", func(t *testing.T) {
		_, err := fetchServiceContext(context.Background(), &http.Client{}, models.Config{}, platform.NewTokenManager("token"), "", "prod")
		if err == nil {
			t.Fatalf("expected missing service id error")
		}
	})

	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/workers/credentials" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:   "svc-1",
					Name: "api",
				},
			})
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		ctxData, err := fetchServiceContext(context.Background(), server.Client(), cfg, platform.NewTokenManager("token"), "svc-1", "prod")
		if err != nil {
			t.Fatalf("unexpected fetch service context error: %v", err)
		}
		if ctxData.Service.ID != "svc-1" {
			t.Fatalf("unexpected service id %q", ctxData.Service.ID)
		}
	})
}

func TestDeleteManagedRepositoryWrapper(t *testing.T) {
	err := deleteManagedRepository(context.Background(), &http.Client{}, nil, models.ServiceConfig{
		SourceType: "registry",
		RepoURL:    "docker.io/releasea/api",
	})
	if err != nil {
		t.Fatalf("expected no-op delete for registry source, got %v", err)
	}

	err = deleteManagedRepository(context.Background(), &http.Client{}, &models.SCMCredential{
		Provider: "github",
		Token:    "",
	}, models.ServiceConfig{
		SourceType:  "git",
		RepoURL:     "https://github.com/releasea/api",
		RepoManaged: true,
	})
	if err == nil {
		t.Fatalf("expected missing token error")
	}
}

func TestHandleServiceDeleteValidation(t *testing.T) {
	err := HandleServiceDelete(context.Background(), &http.Client{}, models.Config{}, platform.NewTokenManager("token"), models.OperationPayload{})
	if err == nil {
		t.Fatalf("expected validation error when service id missing")
	}
}

func TestFetchServiceContextErrorBranches(t *testing.T) {
	t.Run("unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		_, err := fetchServiceContext(context.Background(), server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("token"), "svc-1", "prod")
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
	})

	t.Run("status error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		_, err := fetchServiceContext(context.Background(), server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("token"), "svc-1", "prod")
		if err == nil {
			t.Fatalf("expected status error for credentials fetch")
		}
	})

	t.Run("invalid payload", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{invalid-json"))
		}))
		defer server.Close()

		_, err := fetchServiceContext(context.Background(), server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("token"), "svc-1", "prod")
		if err == nil {
			t.Fatalf("expected decode error for invalid credentials response payload")
		}
	})
}

func TestHandleServiceDeleteNamespaceAndKubeClientErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/workers/credentials" {
			_ = json.NewEncoder(w).Encode(models.DeployContext{
				Service: models.ServiceConfig{
					ID:   "svc-1",
					Name: "",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Run("kube client error with service-name fallback", func(t *testing.T) {
		t.Setenv("RELEASEA_KUBE_TOKEN_FILE", "/tmp/not-found-token")
		err := HandleServiceDelete(
			context.Background(),
			server.Client(),
			models.Config{ApiBaseURL: server.URL},
			platform.NewTokenManager("token"),
			models.OperationPayload{
				Resource:    "svc-1",
				ServiceName: "fallback-name",
				Payload:     map[string]interface{}{"environment": "prod"},
			},
		)
		if err == nil {
			t.Fatalf("expected kube client error for missing service account token file")
		}
	})
}
