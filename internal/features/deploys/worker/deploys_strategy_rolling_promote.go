package deploy

import (
	"context"
	"fmt"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformlogging "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
)

// promoteRollingTraffic switches canonical service routing to the rolling workload
// only after readiness succeeded, avoiding traffic cutover before pods are healthy.
func promoteRollingTraffic(
	ctx context.Context,
	cfg models.Config,
	environment string,
	serviceName string,
	logger *platformlogging.DeployLogger,
) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil
	}

	namespace := shared.ResolveNamespace(cfg, environment)
	kubeHTTP, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return err
	}

	canonicalService, err := platformkube.FetchResourceAsMap(ctx, kubeHTTP, kubeToken, "v1", "Service", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("fetch canonical service failed: %w", err)
	}

	spec := shared.MapValue(canonicalService["spec"])
	selector := shared.MapValue(spec["selector"])
	currentTarget := strings.TrimSpace(shared.StringValue(selector, "app"))
	if currentTarget == serviceName {
		return nil
	}

	platformkube.CleanResourceForReapply(canonicalService)
	RetargetService(canonicalService, serviceName, serviceName)
	if err := platformkube.ApplyResource(ctx, kubeHTTP, kubeToken, canonicalService); err != nil {
		return fmt.Errorf("switch canonical service failed: %w", err)
	}

	if logger != nil {
		logger.Logf(ctx, "rolling promotion: switched canonical service from %s to %s", currentTarget, serviceName)
		logger.Flush(ctx)
	}
	return nil
}
