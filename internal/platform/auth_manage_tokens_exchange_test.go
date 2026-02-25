package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"releaseaworker/internal/models"
	"testing"
)

func TestTokenManagerBootstrapExchangeAndCache(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workers/auth" {
			http.NotFound(w, r)
			return
		}
		requests++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accessToken": "access-from-server",
			"expiresIn":   3600,
		})
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	tokens := NewTokenManager("frg_reg_bootstrap")
	client := server.Client()

	token, err := tokens.Get(context.Background(), client, cfg)
	if err != nil {
		t.Fatalf("unexpected token exchange error: %v", err)
	}
	if token != "access-from-server" {
		t.Fatalf("unexpected exchanged token: %q", token)
	}

	// Second read should use cache and not hit auth endpoint.
	token, err = tokens.Get(context.Background(), client, cfg)
	if err != nil {
		t.Fatalf("unexpected second get error: %v", err)
	}
	if token != "access-from-server" {
		t.Fatalf("unexpected cached token: %q", token)
	}
	if requests != 1 {
		t.Fatalf("expected one exchange request, got %d", requests)
	}
}

func TestExchangeWorkerTokenErrors(t *testing.T) {
	t.Run("http error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		_, _, err := exchangeWorkerToken(context.Background(), server.Client(), cfg, "frg_reg_bootstrap")
		if err == nil {
			t.Fatalf("expected auth status error")
		}
	})

	t.Run("empty access token", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessToken": "",
				"expiresIn":   10,
			})
		}))
		defer server.Close()

		cfg := models.Config{ApiBaseURL: server.URL}
		_, _, err := exchangeWorkerToken(context.Background(), server.Client(), cfg, "frg_reg_bootstrap")
		if err == nil {
			t.Fatalf("expected empty token error")
		}
	})
}
