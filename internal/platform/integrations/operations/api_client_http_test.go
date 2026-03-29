package operations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/platform/auth"
	platformcorrelation "releaseaworker/internal/platform/correlation"
	httpheaders "releaseaworker/internal/platform/http/headers"
	"releaseaworker/internal/platform/models"
	"strings"
	"testing"
)

func TestDoJSONRequestSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-1" {
			t.Fatalf("expected auth header")
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"ok": true,
		})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()

	var out map[string]interface{}
	err := DoJSONRequest(context.Background(), client, cfg, tokens, http.MethodGet, server.URL+"/any", nil, &out, "test request")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["ok"] != true {
		t.Fatalf("unexpected decoded body: %#v", out)
	}
}

func TestDoJSONRequestPropagatesCorrelationID(t *testing.T) {
	var seenCorrelationID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCorrelationID = r.Header.Get(httpheaders.HeaderCorrelationID)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()
	ctx := platformcorrelation.WithID(context.Background(), "corr-test-123")

	var out map[string]interface{}
	if err := DoJSONRequest(ctx, client, cfg, tokens, http.MethodGet, server.URL+"/any", nil, &out, "test request"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenCorrelationID != "corr-test-123" {
		t.Fatalf("expected propagated correlation id, got %q", seenCorrelationID)
	}
}

func TestDoJSONRequestUnauthorizedInvalidatesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()

	err := DoJSONRequest(context.Background(), client, cfg, tokens, http.MethodGet, server.URL+"/any", nil, nil, "test request")
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestUpdateOperationStatusResponses(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		tokens := auth.NewTokenManager("token-1")
		client := server.Client()

		err := UpdateOperationStatus(context.Background(), client, cfg, tokens, "op1", models.OperationStatusInProgress, "")
		if err != ErrOperationConflict {
			t.Fatalf("expected ErrOperationConflict, got %v", err)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		tokens := auth.NewTokenManager("token-1")
		client := server.Client()

		err := UpdateOperationStatus(context.Background(), client, cfg, tokens, "op1", "in-progress", "")
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
	})

	t.Run("failed-status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		tokens := auth.NewTokenManager("token-1")
		client := server.Client()

		err := UpdateOperationStatus(context.Background(), client, cfg, tokens, "op1", "in-progress", "")
		if err == nil || !strings.Contains(err.Error(), "operation update failed") {
			t.Fatalf("expected operation update failed error, got %v", err)
		}
	})
}

func TestClaimOperationSendsLeaseMetadata(t *testing.T) {
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := models.Config{
		ApiBaseURL:             server.URL,
		QueueName:              "releasea.worker",
		OperationClaimLeaseTTL: 180,
	}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()

	if err := ClaimOperation(context.Background(), client, cfg, tokens, "op1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := payload["status"]; got != models.OperationStatusInProgress {
		t.Fatalf("status = %v, want %q", got, models.OperationStatusInProgress)
	}
	claim, ok := payload["claim"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected claim payload, got %#v", payload["claim"])
	}
	if got := claim["ttlSeconds"]; got != float64(180) {
		t.Fatalf("ttlSeconds = %v, want %v", got, 180)
	}
	if got := claim["queueName"]; got != "releasea.worker" {
		t.Fatalf("queueName = %v, want %q", got, "releasea.worker")
	}
}

func TestRecoverStaleOperationClaims(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(models.OperationClaimRecoveryResult{
			Recovered: 2,
			Failed:    1,
			Scanned:   3,
		})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()

	result, err := RecoverStaleOperationClaims(context.Background(), client, cfg, tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Recovered != 2 || result.Failed != 1 || result.Scanned != 3 {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestFetchOperationsByStatusBuildsQuery(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]models.OperationPayload{})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()

	_, err := FetchOperationsByStatus(context.Background(), client, cfg, tokens, models.OperationStatusQueued, models.OperationTypeServiceDeploy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(seenQuery, "status="+models.OperationStatusQueued) || !strings.Contains(seenQuery, "type="+models.OperationTypeServiceDeploy) {
		t.Fatalf("unexpected query built: %q", seenQuery)
	}
}

func TestFetchQueuedOperationsUsesFairnessQuery(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]models.OperationPayload{})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := auth.NewTokenManager("token-1")
	client := server.Client()

	_, err := FetchQueuedOperations(context.Background(), client, cfg, tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(seenQuery, "status="+models.OperationStatusQueued) {
		t.Fatalf("expected queued status in query, got %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "fairness=resource") {
		t.Fatalf("expected fairness=resource in query, got %q", seenQuery)
	}
	if !strings.Contains(seenQuery, "limit=50") {
		t.Fatalf("expected limit=50 in query, got %q", seenQuery)
	}
}
