package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/models"
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
	tokens := NewTokenManager("token-1")
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

func TestDoJSONRequestUnauthorizedInvalidatesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := NewTokenManager("token-1")
	client := server.Client()

	err := DoJSONRequest(context.Background(), client, cfg, tokens, http.MethodGet, server.URL+"/any", nil, nil, "test request")
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
	if tokens.accessToken != "" {
		t.Fatalf("expected token invalidated after unauthorized")
	}
}

func TestUpdateOperationStatusResponses(t *testing.T) {
	t.Run("conflict", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusConflict)
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		tokens := NewTokenManager("token-1")
		client := server.Client()

		err := UpdateOperationStatus(context.Background(), client, cfg, tokens, "op1", "in-progress", "")
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
		tokens := NewTokenManager("token-1")
		client := server.Client()

		err := UpdateOperationStatus(context.Background(), client, cfg, tokens, "op1", "in-progress", "")
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
		if tokens.accessToken != "" {
			t.Fatalf("expected token invalidated")
		}
	})

	t.Run("failed-status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		tokens := NewTokenManager("token-1")
		client := server.Client()

		err := UpdateOperationStatus(context.Background(), client, cfg, tokens, "op1", "in-progress", "")
		if err == nil || !strings.Contains(err.Error(), "operation update failed") {
			t.Fatalf("expected operation update failed error, got %v", err)
		}
	})
}

func TestFetchOperationsByStatusBuildsQuery(t *testing.T) {
	var seenQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]models.OperationPayload{})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := NewTokenManager("token-1")
	client := server.Client()

	_, err := FetchOperationsByStatus(context.Background(), client, cfg, tokens, "queued", "service.deploy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(seenQuery, "status=queued") || !strings.Contains(seenQuery, "type=service.deploy") {
		t.Fatalf("unexpected query built: %q", seenQuery)
	}
}
