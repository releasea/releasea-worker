package deploy

import (
	"context"
	"errors"
	"releaseaworker/internal/models"
	"testing"
)

type timeoutNetErr struct {
	message   string
	timeout   bool
	temporary bool
}

func (e timeoutNetErr) Error() string {
	return e.message
}

func (e timeoutNetErr) Timeout() bool {
	return e.timeout
}

func (e timeoutNetErr) Temporary() bool {
	return e.temporary
}

func TestOperationStrategyType(t *testing.T) {
	op := models.OperationPayload{
		Payload: map[string]interface{}{
			"strategyType": "CANARY",
		},
	}
	if got := OperationStrategyType(op); got != "canary" {
		t.Fatalf("expected canary, got %q", got)
	}
}

func TestStrategyRequiresRollback(t *testing.T) {
	if !StrategyRequiresRollback("canary") {
		t.Fatalf("expected canary rollback")
	}
	if !StrategyRequiresRollback("blue-green") {
		t.Fatalf("expected blue-green rollback")
	}
	if StrategyRequiresRollback("rolling") {
		t.Fatalf("did not expect rolling rollback")
	}
}

func TestIsTransientDeployError(t *testing.T) {
	if IsTransientDeployError(nil) {
		t.Fatalf("nil error must not be transient")
	}
	if !IsTransientDeployError(context.DeadlineExceeded) {
		t.Fatalf("deadline exceeded must be transient")
	}
	if !IsTransientDeployError(timeoutNetErr{message: "timeout", timeout: true}) {
		t.Fatalf("timeout net error must be transient")
	}
	if !IsTransientDeployError(timeoutNetErr{message: "temporary network issue", temporary: true}) {
		t.Fatalf("temporary net error must be transient")
	}
	if !IsTransientDeployError(errors.New("connection reset by peer")) {
		t.Fatalf("connection reset text must be transient")
	}
	if IsTransientDeployError(errors.New("schema validation failed")) {
		t.Fatalf("validation error should not be transient")
	}
}

func TestResolveDeployStrategyType(t *testing.T) {
	service := models.ServiceConfig{
		DeployTemplateID: "tpl-cronjob",
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type: "canary",
		},
	}
	if got := ResolveDeployStrategyType(service); got != "rolling" {
		t.Fatalf("expected rolling for cronjob, got %q", got)
	}
}
