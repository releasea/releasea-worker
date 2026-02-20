package ops

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func promoteBlueGreen(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	service serviceConfig,
	serviceID string,
	environment string,
	serviceName string,
	logger *deployLogger,
) (string, error) {
	activeSlot, candidateSlot := resolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
	activeName := serviceName + "-" + activeSlot
	candidateName := serviceName + "-" + candidateSlot
	namespace := resolveNamespace(cfg, environment)

	kubeClient, kubeToken, err := kubeClient()
	if err != nil {
		return "", err
	}

	if logger != nil {
		logger.Logf(ctx, "blue-green promoting traffic from %s to %s", activeName, candidateName)
	}

	if err := switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, candidateName); err != nil {
		return "", err
	}

	if err := updateBlueGreenActiveSlot(ctx, client, cfg, tokens, serviceID, environment, candidateSlot); err != nil {
		_ = switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, activeName)
		return "", err
	}

	observedService := service
	observedService.DeploymentStrategy.BlueGreenPrimary = candidateSlot
	observationSeconds := envInt("WORKER_BLUE_GREEN_OBSERVATION_SECONDS", 30)
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
		_ = updateBlueGreenActiveSlot(ctx, client, cfg, tokens, serviceID, environment, activeSlot)
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
	canonicalService, err := fetchResourceAsMap(ctx, kubeClient, kubeToken, "v1", "Service", namespace, serviceName)
	if err != nil {
		return fmt.Errorf("fetch canonical service failed: %w", err)
	}
	cleanResourceForReapply(canonicalService)
	meta := mapValue(canonicalService["metadata"])
	meta["name"] = serviceName
	labels := mapValue(meta["labels"])
	if len(labels) > 0 {
		labels["app"] = targetApp
		meta["labels"] = labels
	}
	canonicalService["metadata"] = meta
	spec := mapValue(canonicalService["spec"])
	selector := mapValue(spec["selector"])
	selector["app"] = targetApp
	spec["selector"] = selector
	canonicalService["spec"] = spec
	return applyResource(ctx, kubeClient, kubeToken, canonicalService)
}

func cleanupBlueGreenSlot(
	ctx context.Context,
	kubeClient *http.Client,
	kubeToken string,
	namespace string,
	slotName string,
	logger *deployLogger,
) error {
	if err := deleteResource(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, slotName); err != nil {
		return err
	}
	if err := deleteResource(ctx, kubeClient, kubeToken, "v1", "Service", namespace, slotName); err != nil {
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
	logger *deployLogger,
) error {
	if err := deleteResource(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, serviceName); err != nil {
		return err
	}
	if err := deleteResource(ctx, kubeClient, kubeToken, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName); err != nil {
		return err
	}
	if logger != nil {
		logger.Logf(ctx, "blue-green cleanup completed for legacy workload %s", serviceName)
		logger.Flush(ctx)
	}
	return nil
}
