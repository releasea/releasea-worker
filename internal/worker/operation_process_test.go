package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"
	"testing"
	"time"
)

func newTestOperationProcessor() operationProcessor {
	return operationProcessor{
		fetchOperation: func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string) (models.OperationPayload, error) {
			return models.OperationPayload{}, nil
		},
		updateOperationStatus: func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _, _, _ string) error {
			return nil
		},
		updateDeployStrategyStatus: func(
			_ context.Context,
			_ *http.Client,
			_ models.Config,
			_ *platform.TokenManager,
			_ string,
			_ string,
			_ string,
			_ string,
			_ string,
			_ map[string]interface{},
		) error {
			return nil
		},
		executeOperation: func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ models.OperationPayload) error {
			return nil
		},
		operationStrategyType: func(_ models.OperationPayload) string { return "rolling" },
		isTransientDeployError: func(_ error) bool {
			return false
		},
		strategyRequiresRollback: func(_ string) bool { return false },
		deployRetryDelay:         func() time.Duration { return time.Millisecond },
		deployRetryMaxAttempts:   func() int { return 3 },
		wait: func(_ context.Context, _ time.Duration) error {
			return nil
		},
	}
}

func TestOperationProcessorSkipsAlreadyClaimedOperation(t *testing.T) {
	processor := newTestOperationProcessor()
	var executed bool
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string, status string, _ string) error {
		if status == "in-progress" {
			return errOperationConflict
		}
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ models.OperationPayload) error {
		executed = true
		return nil
	}

	op := models.OperationPayload{ID: "op-claim-conflict", Status: "queued", Type: "service.deploy"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if executed {
		t.Fatalf("expected operation not to execute when claim conflicts")
	}
}

func TestOperationProcessorSkipsNonQueuedOperation(t *testing.T) {
	processor := newTestOperationProcessor()
	statusCalls := 0
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string, _ string, _ string) error {
		statusCalls++
		return nil
	}

	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, models.OperationPayload{
		ID:     "op-non-queued",
		Status: "succeeded",
		Type:   "service.deploy",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if statusCalls != 0 {
		t.Fatalf("expected no status updates for non-queued operation, got %d", statusCalls)
	}
}

func TestOperationProcessorMarksUnknownOperationAsFailed(t *testing.T) {
	processor := newTestOperationProcessor()
	var statuses []string
	var failureMsg string
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string, status string, errMsg string) error {
		statuses = append(statuses, status)
		if status == "failed" {
			failureMsg = errMsg
		}
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, op models.OperationPayload) error {
		return fmt.Errorf("%w: %s", ErrUnsupportedOperationType, op.Type)
	}

	op := models.OperationPayload{ID: "op-unknown", Status: "queued", Type: "unknown.operation"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(statuses) != 2 || statuses[0] != "in-progress" || statuses[1] != "failed" {
		t.Fatalf("expected status transitions [in-progress failed], got %v", statuses)
	}
	if !strings.Contains(failureMsg, ErrUnsupportedOperationType.Error()) {
		t.Fatalf("expected failure message for unknown operation, got %q", failureMsg)
	}
}

func TestOperationProcessorMarksSuccess(t *testing.T) {
	processor := newTestOperationProcessor()
	var statuses []string
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string, status string, _ string) error {
		statuses = append(statuses, status)
		return nil
	}

	op := models.OperationPayload{ID: "op-success", Status: "queued", Type: "service.deploy"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(statuses) != 2 || statuses[0] != "in-progress" || statuses[1] != "succeeded" {
		t.Fatalf("expected [in-progress succeeded], got %v", statuses)
	}
}

func TestOperationProcessorHandlesSuccessConflict(t *testing.T) {
	processor := newTestOperationProcessor()
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string, status string, _ string) error {
		if status == "succeeded" {
			return errOperationConflict
		}
		return nil
	}

	op := models.OperationPayload{ID: "op-success-conflict", Status: "queued", Type: "service.deploy"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error on success conflict, got %v", err)
	}
}

func TestOperationProcessorFailOperationWithRollback(t *testing.T) {
	processor := newTestOperationProcessor()
	execErr := errors.New("boom")
	var strategyStatuses []string
	var operationStatuses []string

	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ models.OperationPayload) error {
		return execErr
	}
	processor.strategyRequiresRollback = func(_ string) bool { return true }
	processor.operationStrategyType = func(_ models.OperationPayload) string { return "blue-green" }
	processor.updateDeployStrategyStatus = func(
		_ context.Context,
		_ *http.Client,
		_ models.Config,
		_ *platform.TokenManager,
		_ string,
		status string,
		_ string,
		_ string,
		_ string,
		_ map[string]interface{},
	) error {
		strategyStatuses = append(strategyStatuses, status)
		return nil
	}
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ string, status string, _ string) error {
		operationStatuses = append(operationStatuses, status)
		return nil
	}

	op := models.OperationPayload{ID: "op-rollback", Status: "queued", Type: "service.deploy", DeployID: "dep-1"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(strategyStatuses) == 0 || strategyStatuses[0] != "rollback" {
		t.Fatalf("expected rollback strategy update, got %v", strategyStatuses)
	}
	if len(operationStatuses) < 2 || operationStatuses[1] != "failed" {
		t.Fatalf("expected failed operation status, got %v", operationStatuses)
	}
}

func TestOperationProcessorFailOperationWithoutRollback(t *testing.T) {
	processor := newTestOperationProcessor()
	execErr := errors.New("permanent inconsistency")
	var strategyStatuses []string
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ models.OperationPayload) error {
		return execErr
	}
	processor.strategyRequiresRollback = func(_ string) bool { return false }
	processor.updateDeployStrategyStatus = func(
		_ context.Context,
		_ *http.Client,
		_ models.Config,
		_ *platform.TokenManager,
		_ string,
		status string,
		_ string,
		_ string,
		_ string,
		_ map[string]interface{},
	) error {
		strategyStatuses = append(strategyStatuses, status)
		return nil
	}

	op := models.OperationPayload{ID: "op-failed", Status: "queued", Type: "service.deploy", DeployID: "dep-2"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(strategyStatuses) == 0 || strategyStatuses[0] != "failed" {
		t.Fatalf("expected failed strategy status, got %v", strategyStatuses)
	}
}

func TestOperationProcessorRetriesTransientServiceDeploy(t *testing.T) {
	processor := newTestOperationProcessor()
	transientErr := errors.New("temporary outage")
	var attempts int
	var waits int
	var retryingUpdates int

	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ models.OperationPayload) error {
		attempts++
		if attempts < 3 {
			return transientErr
		}
		return nil
	}
	processor.isTransientDeployError = func(err error) bool {
		return errors.Is(err, transientErr)
	}
	processor.deployRetryMaxAttempts = func() int { return 3 }
	processor.wait = func(_ context.Context, _ time.Duration) error {
		waits++
		return nil
	}
	processor.updateDeployStrategyStatus = func(
		_ context.Context,
		_ *http.Client,
		_ models.Config,
		_ *platform.TokenManager,
		_ string,
		status string,
		_ string,
		_ string,
		_ string,
		_ map[string]interface{},
	) error {
		if status == "retrying" {
			retryingUpdates++
		}
		return nil
	}

	op := models.OperationPayload{ID: "op-retry", Status: "queued", Type: "service.deploy", DeployID: "dep-1"}
	err := processor.executeWithRetry(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if waits != 2 {
		t.Fatalf("expected 2 waits, got %d", waits)
	}
	if retryingUpdates != 2 {
		t.Fatalf("expected 2 retrying status updates, got %d", retryingUpdates)
	}
}

func TestExecuteWithRetryStopsOnContextCancellation(t *testing.T) {
	processor := newTestOperationProcessor()
	transientErr := errors.New("temporary outage")
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platform.TokenManager, _ models.OperationPayload) error {
		return transientErr
	}
	processor.isTransientDeployError = func(err error) bool {
		return errors.Is(err, transientErr)
	}
	processor.deployRetryMaxAttempts = func() int { return 3 }
	processor.wait = func(_ context.Context, _ time.Duration) error {
		return context.Canceled
	}

	op := models.OperationPayload{ID: "op-cancel", Status: "queued", Type: "service.deploy", DeployID: "dep-3"}
	err := processor.executeWithRetry(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestRollbackSummary(t *testing.T) {
	if got := rollbackSummary("blue-green"); got == "" || !strings.Contains(got, "active environment") {
		t.Fatalf("unexpected blue-green summary: %q", got)
	}
	if got := rollbackSummary("canary"); got == "" || !strings.Contains(got, "gradual exposure") {
		t.Fatalf("unexpected canary summary: %q", got)
	}
	if got := rollbackSummary("rolling"); got != "Restoring the previous version" {
		t.Fatalf("unexpected default summary: %q", got)
	}
}
