package workers

import (
	"context"
	"errors"
	platformauth "releaseaworker/internal/platform/auth"
	"releaseaworker/internal/platform/models"
	"sync"
	"testing"
	"time"
)

func TestRunConsumerWithRetryBackoffProgression(t *testing.T) {
	originalRunner := consumerRunner
	originalWaiter := backoffWaiter
	defer func() {
		consumerRunner = originalRunner
		backoffWaiter = originalWaiter
	}()

	var mu sync.Mutex
	attempts := 0
	waits := make([]time.Duration, 0)

	ctx, cancel := context.WithCancel(context.Background())
	consumerRunner = func(ctx context.Context, _ models.Config, _ *platformauth.TokenManager) error {
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()
		if current < 3 {
			return errors.New("temporary connection error")
		}
		<-ctx.Done()
		return nil
	}
	backoffWaiter = func(_ context.Context, backoff time.Duration) bool {
		mu.Lock()
		waits = append(waits, backoff)
		mu.Unlock()
		return true
	}

	done := make(chan struct{})
	go func() {
		runConsumerWithRetry(ctx, models.Config{}, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		currentAttempts := attempts
		mu.Unlock()
		if currentAttempts >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected at least 3 attempts, got %d", currentAttempts)
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(waits) < 2 {
		t.Fatalf("expected at least 2 backoff waits, got %d", len(waits))
	}
	if waits[0] != 2*time.Second {
		t.Fatalf("expected first backoff 2s, got %s", waits[0])
	}
	if waits[1] != 4*time.Second {
		t.Fatalf("expected second backoff 4s, got %s", waits[1])
	}
}

func TestRunConsumerWithRetryStopsWhenBackoffWaitCanceled(t *testing.T) {
	originalRunner := consumerRunner
	originalWaiter := backoffWaiter
	defer func() {
		consumerRunner = originalRunner
		backoffWaiter = originalWaiter
	}()

	attempts := 0
	consumerRunner = func(_ context.Context, _ models.Config, _ *platformauth.TokenManager) error {
		attempts++
		return errors.New("rabbit unavailable")
	}
	backoffWaiter = func(_ context.Context, _ time.Duration) bool {
		return false
	}

	runConsumerWithRetry(context.Background(), models.Config{}, nil)
	if attempts != 1 {
		t.Fatalf("expected single attempt when wait is canceled, got %d", attempts)
	}
}
