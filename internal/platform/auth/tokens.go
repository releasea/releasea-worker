package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	httpheaders "releaseaworker/internal/platform/http/headers"
	"releaseaworker/internal/platform/models"
)

type TokenManager struct {
	bootstrapToken string
	accessToken    string
	expiresAt      time.Time
	mu             sync.Mutex
}

func NewTokenManager(token string) *TokenManager {
	manager := &TokenManager{}
	if isRegistrationToken(token) {
		manager.bootstrapToken = token
	} else if token != "" {
		manager.accessToken = token
	}
	return manager
}

func (tm *TokenManager) Get(ctx context.Context, client *http.Client, cfg models.Config) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.accessToken != "" {
		if tm.expiresAt.IsZero() || time.Until(tm.expiresAt) > 2*time.Minute {
			return tm.accessToken, nil
		}
	}

	if tm.bootstrapToken == "" {
		if tm.accessToken == "" {
			return "", errors.New("RELEASEA_WORKER_TOKEN not set")
		}
		return tm.accessToken, nil
	}

	accessToken, expiresIn, err := exchangeWorkerToken(ctx, client, cfg, tm.bootstrapToken)
	if err != nil {
		return "", err
	}
	tm.accessToken = accessToken
	if expiresIn > 0 {
		tm.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	}
	return tm.accessToken, nil
}

func (tm *TokenManager) Invalidate() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.accessToken = ""
	tm.expiresAt = time.Time{}
}

func SetAuthHeaders(req *http.Request, token string) {
	httpheaders.SetBearerToken(req, token)
}

func exchangeWorkerToken(ctx context.Context, client *http.Client, cfg models.Config, bootstrapToken string) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ApiBaseURL+"/workers/auth", bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	SetAuthHeaders(req, bootstrapToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", 0, fmt.Errorf("worker auth failed: %s", resp.Status)
	}

	var body struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int    `json:"expiresIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", 0, err
	}
	if body.AccessToken == "" {
		return "", 0, errors.New("worker auth returned empty token")
	}
	return body.AccessToken, body.ExpiresIn, nil
}

func isRegistrationToken(token string) bool {
	return strings.HasPrefix(token, "frg_reg_")
}
