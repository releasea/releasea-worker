package worker

import (
	"context"
	"errors"
	"net/http"
	"releaseaworker/internal/models"
	"testing"
	"time"
)

func TestExecuteOperationUnknownTypeReturnsErrorImmediately(t *testing.T) {
	start := time.Now()
	err := executeOperation(
		context.Background(),
		&http.Client{},
		models.Config{},
		nil,
		models.OperationPayload{Type: "unknown.operation"},
	)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrUnsupportedOperationType) {
		t.Fatalf("expected ErrUnsupportedOperationType, got %v", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected no blocking sleep for unknown operation, elapsed=%s", elapsed)
	}
}
