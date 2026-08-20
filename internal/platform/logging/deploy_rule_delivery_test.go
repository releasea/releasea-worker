package logging

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestDeployLoggerRetriesBatchWithStableID(t *testing.T) {
	logger := &DeployLogger{deployID: "deploy-1", maxBatch: 50}
	var ids []string
	var received [][]string
	logger.sendBatch = func(_ context.Context, batchID string, lines []string) error {
		ids = append(ids, batchID)
		received = append(received, append([]string(nil), lines...))
		if len(ids) == 1 {
			return errors.New("response lost")
		}
		return nil
	}

	logger.AppendLines(context.Background(), []string{"first", "second"})
	logger.Flush(context.Background())

	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("batch IDs = %v, want two equal non-empty IDs", ids)
	}
	for _, lines := range received {
		if !reflect.DeepEqual(lines, []string{"first", "second"}) {
			t.Fatalf("received lines = %v", lines)
		}
	}
	if len(logger.pending) != 0 {
		t.Fatalf("pending batches = %d, want 0", len(logger.pending))
	}
}

func TestDeployLoggerRetainsBatchAfterRetryExhaustion(t *testing.T) {
	logger := &DeployLogger{deployID: "deploy-1", maxBatch: 50}
	logger.sendBatch = func(context.Context, string, []string) error {
		return errors.New("unavailable")
	}

	logger.AppendLines(context.Background(), []string{"keep me"})
	logger.Flush(context.Background())
	if len(logger.pending) != 1 {
		t.Fatalf("pending batches = %d, want 1", len(logger.pending))
	}
	wantID := logger.pending[0].id

	var retriedID string
	logger.sendBatch = func(_ context.Context, batchID string, _ []string) error {
		retriedID = batchID
		return nil
	}
	logger.Flush(context.Background())
	if retriedID != wantID {
		t.Fatalf("retried batch ID = %q, want %q", retriedID, wantID)
	}
	if len(logger.pending) != 0 {
		t.Fatalf("pending batches = %d, want 0", len(logger.pending))
	}
}
