package workers

import (
	"context"
	"errors"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	"releaseaworker/internal/platform/models"
	"testing"
	"time"
)

func TestWorkerPoolBlocksClaims(t *testing.T) {
	if workerPoolBlocksClaims(workerPoolControl{}) {
		t.Fatalf("expected empty control to allow claims")
	}
	if !workerPoolBlocksClaims(workerPoolControl{DrainEnabled: true}) {
		t.Fatalf("expected drain mode to block claims")
	}
	if !workerPoolBlocksClaims(workerPoolControl{MaintenanceEnabled: true}) {
		t.Fatalf("expected maintenance mode to block claims")
	}
}

func TestWorkerPoolControlCacheReturnsCachedValueOnFetchFailure(t *testing.T) {
	previousFetch := fetchWorkerPoolControlRequest
	previousTTL := poolControlCacheTTL
	defer func() {
		fetchWorkerPoolControlRequest = previousFetch
		poolControlCacheTTL = previousTTL
	}()

	poolControlCacheTTL = time.Millisecond
	cache := &workerPoolControlCache{
		value:    workerPoolControl{ID: "pool-1", DrainEnabled: true},
		loadedAt: time.Now().Add(-time.Second),
		hasValue: true,
	}

	fetchWorkerPoolControlRequest = func(context.Context, *http.Client, models.Config, *platformauth.TokenManager) (workerPoolControl, error) {
		return workerPoolControl{}, errors.New("boom")
	}

	got, err := cache.get(context.Background(), &http.Client{}, models.Config{}, nil)
	if err != nil {
		t.Fatalf("expected cached value, got error %v", err)
	}
	if !got.DrainEnabled {
		t.Fatalf("expected cached drain state to be preserved")
	}
}
