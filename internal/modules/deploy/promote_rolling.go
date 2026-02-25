package deploy

import (
	"context"
	"fmt"
	"releaseaworker/internal/models"
	"releaseaworker/internal/modules/platform"
	"releaseaworker/internal/modules/shared"
	"strings"

	strategyworker "releaseaworker/internal/modules/deploy/strategy"
)

// promoteRollingTraffic switches canonical service routing to the rolling workload
// only after readiness succeeded, avoiding traffic cutover before pods are healthy.
func promoteRollingTraffic(
	ctx context.Context,
	cfg models.Config,
	environment string,
	serviceName string,
	logger *platform.DeployLogger,
) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil
	}

	namespace := shared.ResolveNamespace(cfg, environment)
	kubeHTTP, kubeToken, err := platform.KubeClient()
	if err != nil {
		return err
	}

	canonicalService, err := platform.FetchResourceAsMap(ctx, kubeHTTP, kubeToken, "v1", "Service", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("fetch canonical service failed: %w", err)
	}

	spec := shared.MapValue(canonicalService["spec"])
	selector := shared.MapValue(spec["selector"])
	currentTarget := strings.TrimSpace(shared.StringValue(selector, "app"))
	if currentTarget == serviceName {
		return nil
	}

	platform.CleanResourceForReapply(canonicalService)
	strategyworker.RetargetService(canonicalService, serviceName, serviceName)
	if err := platform.ApplyResource(ctx, kubeHTTP, kubeToken, canonicalService); err != nil {
		return fmt.Errorf("switch canonical service failed: %w", err)
	}

	if logger != nil {
		logger.Logf(ctx, "rolling promotion: switched canonical service from %s to %s", currentTarget, serviceName)
		logger.Flush(ctx)
	}
	return nil
}
