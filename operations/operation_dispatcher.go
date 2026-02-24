package operations

import (
	"context"
	"net/http"
	"time"
)

type operationExecutor func(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error

var operationExecutors = map[string]operationExecutor{
	"service.deploy":         handleServiceDeploy,
	"service.promote-canary": handlePromoteCanary,
	"service.delete":         handleServiceDelete,
	"rule.deploy":            handleRuleDeploy,
	"rule.publish":           handleRuleDeploy,
	"rule.delete":            handleRuleDelete,
	"worker.restart":         executeWorkerRestart,
}

func executeOperation(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	executor, ok := operationExecutors[op.Type]
	if !ok {
		// Preserve previous behavior for unknown operation types.
		time.Sleep(1 * time.Second)
		return nil
	}
	return executor(ctx, client, cfg, tokens, op)
}

func executeWorkerRestart(ctx context.Context, _ *http.Client, cfg Config, _ *tokenManager, op operationPayload) error {
	return restartDeployment(ctx, cfg, op.Payload)
}
