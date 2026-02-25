package workers

import (
	"context"
	"errors"
	"net/http"
	"releaseaworker/internal/platform/models"
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

func TestDefaultOperationExecutorsMatchSupportedOperationTypes(t *testing.T) {
	supported := models.SupportedOperationTypes()
	if len(defaultOperationExecutors) != len(supported) {
		t.Fatalf("executor count = %d, supported types = %d", len(defaultOperationExecutors), len(supported))
	}

	for _, operationType := range supported {
		executor, ok := defaultOperationExecutors[operationType]
		if !ok {
			t.Fatalf("missing executor for operation type %s", operationType)
		}
		if executor == nil {
			t.Fatalf("executor for operation type %s is nil", operationType)
		}
	}
}
