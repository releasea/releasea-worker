package config

import (
	"reflect"
	"testing"
	"time"
)

func TestParseTags(t *testing.T) {
	got := parseTags(" api, worker , ,deploy ")
	want := []string{"api", "worker", "deploy"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if parseTags("") != nil {
		t.Fatalf("expected nil for empty tags")
	}
}

func TestLoadConfigFromEnv(t *testing.T) {
	t.Setenv("RELEASEA_API_BASE_URL", "http://localhost:8080/api/v1/")
	t.Setenv("HEARTBEAT_INTERVAL_SECONDS", "15")
	t.Setenv("WORKER_NAME", "worker-a")
	t.Setenv("WORKER_ID", "")
	t.Setenv("WORKER_TAGS", "edge,primary")
	t.Setenv("WORKER_POLL_SECONDS", "7")
	t.Setenv("WORKER_POLL_LIMIT", "5")
	t.Setenv("WORKER_QUEUE_PREFETCH", "3")
	t.Setenv("WORKER_OPERATION_CLAIM_LEASE_SECONDS", "180")
	t.Setenv("WORKER_TOKEN_REFRESH_SKEW_SECONDS", "240")

	cfg := Load()
	if cfg.ApiBaseURL != "http://localhost:8080/api/v1" {
		t.Fatalf("expected api base without trailing slash, got %q", cfg.ApiBaseURL)
	}
	if cfg.HeartbeatInterval != 15*time.Second {
		t.Fatalf("expected heartbeat 15s, got %s", cfg.HeartbeatInterval)
	}
	if cfg.WorkerID != "worker-a" {
		t.Fatalf("expected worker id fallback to worker name, got %q", cfg.WorkerID)
	}
	if !reflect.DeepEqual(cfg.Tags, []string{"edge", "primary"}) {
		t.Fatalf("unexpected parsed tags: %v", cfg.Tags)
	}
	if cfg.PollInterval != 7*time.Second || cfg.PollBatchLimit != 5 {
		t.Fatalf("unexpected poll settings: interval=%s limit=%d", cfg.PollInterval, cfg.PollBatchLimit)
	}
	if cfg.QueuePrefetch != 3 {
		t.Fatalf("expected queue prefetch 3, got %d", cfg.QueuePrefetch)
	}
	if cfg.OperationClaimLeaseTTL != 180 {
		t.Fatalf("expected operation claim lease ttl 180, got %d", cfg.OperationClaimLeaseTTL)
	}
	if cfg.TokenRefreshSkew != 240*time.Second {
		t.Fatalf("expected token refresh skew 240s, got %s", cfg.TokenRefreshSkew)
	}
}
