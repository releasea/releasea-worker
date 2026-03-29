package workers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	"releaseaworker/internal/platform/models"
	"strings"
	"testing"
	"time"
)

func newTestOperationProcessor() operationProcessor {
	return operationProcessor{
		fetchOperation: func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) (models.OperationPayload, error) {
			return models.OperationPayload{}, nil
		},
		claimOperationStatus: func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
			return nil
		},
		updateOperationStatus: func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _, _, _ string) error {
			return nil
		},
		updateDeployStrategyStatus: func(
			_ context.Context,
			_ *http.Client,
			_ models.Config,
			_ *platformauth.TokenManager,
			_ string,
			_ string,
			_ string,
			_ string,
			_ string,
			_ map[string]interface{},
		) error {
			return nil
		},
		executeOperation: func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
			return nil
		},
		operationStrategyType: func(_ models.OperationPayload) string { return "rolling" },
		isTransientDeployError: func(_ error) bool {
			return false
		},
		strategyRequiresRollback: func(_ string) bool { return false },
		rollbackDetected:         func(_ error) bool { return false },
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
	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		return errOperationConflict
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		executed = true
		return nil
	}

	op := models.OperationPayload{ID: "op-claim-conflict", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if executed {
		t.Fatalf("expected operation not to execute when claim conflicts")
	}
}

func TestOperationProcessorRequeuesIncompatibleTaggedOperation(t *testing.T) {
	processor := newTestOperationProcessor()
	statusCalls := 0
	executed := false
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, _ string, _ string) error {
		statusCalls++
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		executed = true
		return nil
	}

	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{
		WorkerName:  "worker-dev-build",
		Environment: "dev",
		Tags:        []string{"build"},
	}, nil, models.OperationPayload{
		ID:     "op-tag-mismatch",
		Status: models.OperationStatusQueued,
		Type:   models.OperationTypeServiceDeploy,
		Payload: map[string]interface{}{
			"environment": "dev",
			"workerTags":  []interface{}{"gpu"},
		},
	})
	if !errors.Is(err, ErrOperationNotCompatible) {
		t.Fatalf("expected ErrOperationNotCompatible, got %v", err)
	}
	if statusCalls != 0 {
		t.Fatalf("expected no status update before requeue, got %d", statusCalls)
	}
	if executed {
		t.Fatalf("expected incompatible operation not to execute")
	}
}

func TestOperationProcessorRequeuesIncompatibleEnvironmentOperation(t *testing.T) {
	processor := newTestOperationProcessor()
	statusCalls := 0
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, _ string, _ string) error {
		statusCalls++
		return nil
	}

	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{
		WorkerName:  "worker-dev",
		Environment: "dev",
	}, nil, models.OperationPayload{
		ID:     "op-env-mismatch",
		Status: models.OperationStatusQueued,
		Type:   models.OperationTypeServiceDelete,
		Payload: map[string]interface{}{
			"environment": "prod",
		},
	})
	if !errors.Is(err, ErrOperationNotCompatible) {
		t.Fatalf("expected ErrOperationNotCompatible, got %v", err)
	}
	if statusCalls != 0 {
		t.Fatalf("expected no status update before requeue, got %d", statusCalls)
	}
}

func TestOperationProcessorRequeuesNonPreferredClusterOperation(t *testing.T) {
	processor := newTestOperationProcessor()
	statusCalls := 0
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, _ string, _ string) error {
		statusCalls++
		return nil
	}

	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{
		WorkerName:  "worker-prod-west",
		Environment: "prod",
		Cluster:     "cluster-west",
	}, nil, models.OperationPayload{
		ID:     "op-cluster-preference",
		Status: models.OperationStatusQueued,
		Type:   models.OperationTypeServiceDeploy,
		Payload: map[string]interface{}{
			"environment":            "prod",
			"preferredWorkerCluster": "cluster-east",
		},
	})
	if !errors.Is(err, ErrOperationNotCompatible) {
		t.Fatalf("expected ErrOperationNotCompatible, got %v", err)
	}
	if statusCalls != 0 {
		t.Fatalf("expected no status update before requeue, got %d", statusCalls)
	}
}

func TestOperationProcessorSkipsNonQueuedOperation(t *testing.T) {
	processor := newTestOperationProcessor()
	statusCalls := 0
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, _ string, _ string) error {
		statusCalls++
		return nil
	}

	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, models.OperationPayload{
		ID:     "op-non-queued",
		Status: models.OperationStatusSucceeded,
		Type:   models.OperationTypeServiceDeploy,
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
	var claimCalls int
	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		claimCalls++
		return nil
	}
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, errMsg string) error {
		statuses = append(statuses, status)
		if status == models.OperationStatusFailed {
			failureMsg = errMsg
		}
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, op models.OperationPayload) error {
		return fmt.Errorf("%w: %s", ErrUnsupportedOperationType, op.Type)
	}

	op := models.OperationPayload{ID: "op-unknown", Status: models.OperationStatusQueued, Type: "unknown.operation"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if claimCalls != 1 {
		t.Fatalf("expected single claim call, got %d", claimCalls)
	}
	if len(statuses) != 1 || statuses[0] != models.OperationStatusFailed {
		t.Fatalf("expected failed status update after claim, got %v", statuses)
	}
	if !strings.Contains(failureMsg, ErrUnsupportedOperationType.Error()) {
		t.Fatalf("expected failure message for unknown operation, got %q", failureMsg)
	}
}

func TestOperationProcessorMarksSuccess(t *testing.T) {
	processor := newTestOperationProcessor()
	var statuses []string
	var claimCalls int
	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		claimCalls++
		return nil
	}
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, _ string) error {
		statuses = append(statuses, status)
		return nil
	}

	op := models.OperationPayload{ID: "op-success", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if claimCalls != 1 {
		t.Fatalf("expected single claim call, got %d", claimCalls)
	}
	if len(statuses) != 1 || statuses[0] != models.OperationStatusSucceeded {
		t.Fatalf("expected [succeeded] after claim, got %v", statuses)
	}
}

func TestOperationProcessorHandlesSuccessConflict(t *testing.T) {
	processor := newTestOperationProcessor()
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, _ string) error {
		if status == models.OperationStatusSucceeded {
			return errOperationConflict
		}
		return nil
	}

	op := models.OperationPayload{ID: "op-success-conflict", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy}
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
	var claimCalls int

	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		claimCalls++
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		return execErr
	}
	processor.strategyRequiresRollback = func(_ string) bool { return true }
	processor.rollbackDetected = func(err error) bool {
		return errors.Is(err, execErr)
	}
	processor.operationStrategyType = func(_ models.OperationPayload) string { return "blue-green" }
	processor.updateDeployStrategyStatus = func(
		_ context.Context,
		_ *http.Client,
		_ models.Config,
		_ *platformauth.TokenManager,
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
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, _ string) error {
		operationStatuses = append(operationStatuses, status)
		return nil
	}

	op := models.OperationPayload{ID: "op-rollback", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy, DeployID: "dep-1"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(strategyStatuses) == 0 || strategyStatuses[0] != "rollback" {
		t.Fatalf("expected rollback strategy update, got %v", strategyStatuses)
	}
	if claimCalls != 1 {
		t.Fatalf("expected single claim call, got %d", claimCalls)
	}
	if len(operationStatuses) != 1 || operationStatuses[0] != models.OperationStatusFailed {
		t.Fatalf("expected failed operation status, got %v", operationStatuses)
	}
}

func TestOperationProcessorTreatsStrategyFailureAsFailedWithoutRollbackSignal(t *testing.T) {
	processor := newTestOperationProcessor()
	execErr := errors.New("repository authentication failed")
	var strategyStatuses []string

	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		return execErr
	}
	processor.strategyRequiresRollback = func(_ string) bool { return true }
	processor.rollbackDetected = func(_ error) bool { return false }
	processor.operationStrategyType = func(_ models.OperationPayload) string { return "canary" }
	processor.updateDeployStrategyStatus = func(
		_ context.Context,
		_ *http.Client,
		_ models.Config,
		_ *platformauth.TokenManager,
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

	op := models.OperationPayload{ID: "op-strategy-failed", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy, DeployID: "dep-3"}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(strategyStatuses) == 0 || strategyStatuses[0] != "failed" {
		t.Fatalf("expected failed strategy update when rollback not performed, got %v", strategyStatuses)
	}
}

func TestOperationProcessorFailOperationWithoutRollback(t *testing.T) {
	processor := newTestOperationProcessor()
	execErr := errors.New("permanent inconsistency")
	var strategyStatuses []string
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		return execErr
	}
	processor.strategyRequiresRollback = func(_ string) bool { return false }
	processor.updateDeployStrategyStatus = func(
		_ context.Context,
		_ *http.Client,
		_ models.Config,
		_ *platformauth.TokenManager,
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

	op := models.OperationPayload{ID: "op-failed", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy, DeployID: "dep-2"}
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

	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
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
		_ *platformauth.TokenManager,
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

	op := models.OperationPayload{ID: "op-retry", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy, DeployID: "dep-1"}
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
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		return transientErr
	}
	processor.isTransientDeployError = func(err error) bool {
		return errors.Is(err, transientErr)
	}
	processor.deployRetryMaxAttempts = func() int { return 3 }
	processor.wait = func(_ context.Context, _ time.Duration) error {
		return context.Canceled
	}

	op := models.OperationPayload{ID: "op-cancel", Status: models.OperationStatusQueued, Type: models.OperationTypeServiceDeploy, DeployID: "dep-3"}
	err := processor.executeWithRetry(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}

func TestOperationProcessorProcessesRuleDeploySuccessfully(t *testing.T) {
	processor := newTestOperationProcessor()
	var statuses []string
	var executions int
	var claimCalls int
	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		claimCalls++
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		executions++
		return nil
	}
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, _ string) error {
		statuses = append(statuses, status)
		return nil
	}

	op := models.OperationPayload{
		ID:           "op-rule-apply",
		Status:       models.OperationStatusQueued,
		Type:         models.OperationTypeRuleDeploy,
		RuleDeployID: "rdep-1",
	}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if executions != 1 {
		t.Fatalf("expected one execution, got %d", executions)
	}
	if claimCalls != 1 {
		t.Fatalf("expected single claim call, got %d", claimCalls)
	}
	if want := []string{models.OperationStatusSucceeded}; !equalStatuses(statuses, want) {
		t.Fatalf("unexpected status transitions: got %v want %v", statuses, want)
	}
}

func TestOperationProcessorProcessesRuleDeleteSuccessfully(t *testing.T) {
	processor := newTestOperationProcessor()
	var statuses []string
	var claimCalls int
	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		claimCalls++
		return nil
	}
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, _ string) error {
		statuses = append(statuses, status)
		return nil
	}

	op := models.OperationPayload{
		ID:           "op-rule-remove",
		Status:       models.OperationStatusQueued,
		Type:         models.OperationTypeRuleDelete,
		RuleDeployID: "rdep-2",
	}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if claimCalls != 1 {
		t.Fatalf("expected single claim call, got %d", claimCalls)
	}
	if want := []string{models.OperationStatusSucceeded}; !equalStatuses(statuses, want) {
		t.Fatalf("unexpected status transitions: got %v want %v", statuses, want)
	}
}

func TestOperationProcessorRetriesRuleDeployThenFails(t *testing.T) {
	processor := newTestOperationProcessor()
	transientErr := errors.New("temporary upstream outage")
	var attempts int
	var waits int
	var statuses []string
	var failedMessage string
	var strategyUpdateCalls int
	var claimCalls int

	processor.claimOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string) error {
		claimCalls++
		return nil
	}
	processor.executeOperation = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ models.OperationPayload) error {
		attempts++
		return transientErr
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
		_ *platformauth.TokenManager,
		_ string,
		_ string,
		_ string,
		_ string,
		_ string,
		_ map[string]interface{},
	) error {
		strategyUpdateCalls++
		return nil
	}
	processor.updateOperationStatus = func(_ context.Context, _ *http.Client, _ models.Config, _ *platformauth.TokenManager, _ string, status string, errMsg string) error {
		statuses = append(statuses, status)
		if status == models.OperationStatusFailed {
			failedMessage = errMsg
		}
		return nil
	}

	op := models.OperationPayload{
		ID:           "op-rule-retry-failed",
		Status:       models.OperationStatusQueued,
		Type:         models.OperationTypeRuleDeploy,
		RuleDeployID: "rdep-3",
	}
	err := processor.processOperation(context.Background(), &http.Client{}, models.Config{}, nil, op)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
	if waits != 2 {
		t.Fatalf("expected 2 waits, got %d", waits)
	}
	if strategyUpdateCalls != 0 {
		t.Fatalf("expected no deploy strategy updates for rule retry, got %d", strategyUpdateCalls)
	}
	if claimCalls != 1 {
		t.Fatalf("expected single claim call, got %d", claimCalls)
	}
	if want := []string{models.OperationStatusFailed}; !equalStatuses(statuses, want) {
		t.Fatalf("unexpected status transitions: got %v want %v", statuses, want)
	}
	if failedMessage == "" || !strings.Contains(failedMessage, transientErr.Error()) {
		t.Fatalf("expected failed message to include transient error, got %q", failedMessage)
	}
}

func equalStatuses(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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
