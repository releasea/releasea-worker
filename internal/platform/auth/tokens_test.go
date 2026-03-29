package auth

import (
	"context"
	"net/http"
	"releaseaworker/internal/platform/models"
	"testing"
	"time"
)

func TestIsRegistrationToken(t *testing.T) {
	if !isRegistrationToken("frg_reg_123") {
		t.Fatalf("expected registration token prefix match")
	}
	if isRegistrationToken("plain-token") {
		t.Fatalf("did not expect plain token match")
	}
}

func TestNewTokenManager(t *testing.T) {
	manager := NewTokenManager("frg_reg_bootstrap")
	if manager.bootstrapToken == "" || manager.accessToken != "" {
		t.Fatalf("expected bootstrap token manager")
	}

	manager = NewTokenManager("access-token")
	if manager.accessToken == "" || manager.bootstrapToken != "" {
		t.Fatalf("expected access token manager")
	}
}

func TestTokenManagerGetAndInvalidate(t *testing.T) {
	manager := NewTokenManager("access-token")
	token, err := manager.Get(context.Background(), &http.Client{}, models.Config{TokenRefreshSkew: 2 * time.Minute})
	if err != nil {
		t.Fatalf("unexpected get error: %v", err)
	}
	if token != "access-token" {
		t.Fatalf("expected access token, got %q", token)
	}

	manager.expiresAt = time.Now().Add(10 * time.Minute)
	manager.Invalidate()
	if manager.accessToken != "" || !manager.expiresAt.IsZero() {
		t.Fatalf("expected token manager invalidated")
	}
}

func TestTokenManagerGetWithoutToken(t *testing.T) {
	manager := NewTokenManager("")
	_, err := manager.Get(context.Background(), &http.Client{}, models.Config{TokenRefreshSkew: 2 * time.Minute})
	if err == nil {
		t.Fatalf("expected missing token error")
	}
}

func TestTokenManagerRefreshesWhenTokenNearExpiry(t *testing.T) {
	manager := NewTokenManager("frg_reg_bootstrap")
	manager.accessToken = "cached-access-token"
	manager.expiresAt = time.Now().Add(30 * time.Second)

	token, err := manager.Get(context.Background(), &http.Client{}, models.Config{TokenRefreshSkew: 45 * time.Second})
	if err == nil {
		t.Fatalf("expected bootstrap exchange error when cached token is inside refresh skew")
	}
	if token != "" {
		t.Fatalf("expected no token on refresh failure, got %q", token)
	}
}

func TestSetAuthHeaders(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("unexpected request error: %v", err)
	}
	SetAuthHeaders(req, "abc")
	if got := req.Header.Get("Authorization"); got != "Bearer abc" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
}
