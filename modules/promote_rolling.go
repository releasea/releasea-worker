package ops

import (
	"context"
	"fmt"
	"strings"

	strategyworker "releaseaworker/modules/strategy"
)

// promoteRollingTraffic switches canonical service routing to the rolling workload
// only after readiness succeeded, avoiding traffic cutover before pods are healthy.
func promoteRollingTraffic(
	ctx context.Context,
	cfg Config,
	environment string,
	serviceName string,
	logger *deployLogger,
) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil
	}

	namespace := resolveNamespace(cfg, environment)
	kubeHTTP, kubeToken, err := kubeClient()
	if err != nil {
		return err
	}

	canonicalService, err := fetchResourceAsMap(ctx, kubeHTTP, kubeToken, "v1", "Service", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("fetch canonical service failed: %w", err)
	}

	spec := mapValue(canonicalService["spec"])
	selector := mapValue(spec["selector"])
	currentTarget := strings.TrimSpace(stringValue(selector, "app"))
	if currentTarget == serviceName {
		return nil
	}

	cleanResourceForReapply(canonicalService)
	strategyworker.RetargetService(canonicalService, serviceName, serviceName)
	if err := applyResource(ctx, kubeHTTP, kubeToken, canonicalService); err != nil {
		return fmt.Errorf("switch canonical service failed: %w", err)
	}

	if logger != nil {
		logger.Logf(ctx, "rolling promotion: switched canonical service from %s to %s", currentTarget, serviceName)
		logger.Flush(ctx)
	}
	return nil
}
