package deploy

import (
	"context"
	"fmt"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	platformlogging "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"time"
)

func promoteBlueGreen(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	service models.ServiceConfig,
	serviceID string,
	environment string,
	serviceName string,
	logger *platformlogging.DeployLogger,
) (string, error) {
	activeSlot, candidateSlot := ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
	activeName := serviceName + "-" + activeSlot
	candidateName := serviceName + "-" + candidateSlot
	namespace := shared.ResolveNamespace(cfg, environment)

	kubeClient, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return "", err
	}

	if logger != nil {
		logger.Logf(ctx, "blue-green promoting traffic from %s to %s", activeName, candidateName)
	}

	if err := switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, candidateName); err != nil {
		return "", err
	}

	if err := platformops.UpdateBlueGreenActiveSlot(ctx, client, cfg, tokens, serviceID, environment, candidateSlot); err != nil {
		_ = switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, activeName)
		return "", err
	}

	observedService := service
	observedService.DeploymentStrategy.BlueGreenPrimary = candidateSlot
	observationSeconds := shared.EnvInt("WORKER_BLUE_GREEN_OBSERVATION_SECONDS", 30)
	if observationSeconds < 0 {
		observationSeconds = 0
	}

	if err := waitForServiceDeployReadiness(
		ctx,
		cfg,
		environment,
		namespace,
		serviceName,
		[]string{candidateName},
		observedService,
		logger,
	); err != nil {
		_ = switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, activeName)
		_ = platformops.UpdateBlueGreenActiveSlot(ctx, client, cfg, tokens, serviceID, environment, activeSlot)
		return "", fmt.Errorf("blue-green promoted slot unhealthy: %w", err)
	}

	if observationSeconds > 0 {
		if logger != nil {
			logger.Logf(ctx, "blue-green observation period started (%ds)", observationSeconds)
			logger.Flush(ctx)
		}
		timer := time.NewTimer(time.Duration(observationSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}

	if err := cleanupBlueGreenSlot(ctx, kubeClient, kubeToken, namespace, activeName, logger); err != nil {
		if logger != nil {
			logger.Logf(ctx, "blue-green cleanup warning for %s: %v", activeName, err)
			logger.Flush(ctx)
		}
	}
	if err := cleanupLegacyRollingWorkload(ctx, kubeClient, kubeToken, namespace, serviceName, logger); err != nil {
		if logger != nil {
			logger.Logf(ctx, "blue-green cleanup warning for legacy workload %s: %v", serviceName, err)
			logger.Flush(ctx)
		}
	}

	return candidateSlot, nil
}

func switchBlueGreenCanonicalService(
	ctx context.Context,
	kubeClient *http.Client,
	kubeToken string,
	namespace string,
	serviceName string,
	targetApp string,
) error {
	canonicalService, err := platformkube.FetchResourceAsMap(ctx, kubeClient, kubeToken, "v1", "Service", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("fetch canonical service failed: %w", err)
	}
	platformkube.CleanResourceForReapply(canonicalService)
	meta := shared.MapValue(canonicalService["metadata"])
	meta["name"] = serviceName
	labels := shared.MapValue(meta["labels"])
	if len(labels) > 0 {
		labels["app"] = targetApp
		meta["labels"] = labels
	}
	canonicalService["metadata"] = meta
	spec := shared.MapValue(canonicalService["spec"])
	selector := shared.MapValue(spec["selector"])
	selector["app"] = targetApp
	spec["selector"] = selector
	canonicalService["spec"] = spec
	return platformkube.ApplyResource(ctx, kubeClient, kubeToken, canonicalService)
}

func cleanupBlueGreenSlot(
	ctx context.Context,
	kubeClient *http.Client,
	kubeToken string,
	namespace string,
	slotName string,
	logger *platformlogging.DeployLogger,
) error {
	if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, slotName); err != nil {
		return err
	}
	if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "v1", "Service", namespace, slotName); err != nil {
		return err
	}
	if logger != nil {
		logger.Logf(ctx, "blue-green cleanup completed for %s", slotName)
		logger.Flush(ctx)
	}
	return nil
}

func cleanupLegacyRollingWorkload(
	ctx context.Context,
	kubeClient *http.Client,
	kubeToken string,
	namespace string,
	serviceName string,
	logger *platformlogging.DeployLogger,
) error {
	if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, serviceName); err != nil {
		return err
	}
	if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName); err != nil {
		return err
	}
	if logger != nil {
		logger.Logf(ctx, "blue-green cleanup completed for legacy workload %s", serviceName)
		logger.Flush(ctx)
	}
	return nil
}
