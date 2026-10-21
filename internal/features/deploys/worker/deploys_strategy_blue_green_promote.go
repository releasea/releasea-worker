package deploy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	platformlogging "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
	"time"
)

func blueGreenSlotTitle(slot string) string {
	if strings.EqualFold(strings.TrimSpace(slot), "green") {
		return "Green"
	}
	return "Blue"
}

type blueGreenStepReporter func(step, summary string)

func HandleSwitchBlueGreen(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	if op.Resource == "" {
		return errors.New("service id missing")
	}
	environment := platformops.PayloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}
	contextData, err := fetchServiceContext(ctx, client, cfg, tokens, op.Resource, environment)
	if err != nil {
		return err
	}
	if ResolveDeployStrategyType(contextData.Service) != "blue-green" {
		return fmt.Errorf("service is not using blue-green strategy")
	}
	activeSlot := strings.ToLower(strings.TrimSpace(platformops.PayloadString(op.Payload, "activeSlot")))
	if activeSlot == "blue" || activeSlot == "green" {
		contextData.Service.DeploymentStrategy.BlueGreenPrimary = activeSlot
	}
	serviceName := shared.ToKubeName(contextData.Service.Name)
	if serviceName == "" {
		serviceName = shared.ToKubeName(contextData.Service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}
	logger := platformlogging.NewDeployLogger(client, cfg, tokens, op.DeployID)
	if logger != nil {
		defer logger.Flush(ctx)
	}
	_, err = promoteBlueGreen(
		ctx,
		client,
		cfg,
		tokens,
		contextData.Service,
		op.Resource,
		environment,
		serviceName,
		logger,
		func(step, summary string) {
			if logger != nil {
				logger.Logf(ctx, "blue-green switch %s: %s", step, summary)
			}
			if strings.TrimSpace(op.DeployID) == "" {
				return
			}
			if updateErr := platformops.UpdateDeployStrategyStatus(ctx, client, cfg, tokens, op.DeployID, "", "blue-green", "promoting", summary, map[string]interface{}{"step": step}); updateErr != nil {
				log.Printf("[worker] blue-green switch step %s: %v", step, updateErr)
			}
		},
	)
	return err
}

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
	report ...blueGreenStepReporter,
) (string, error) {
	var notify blueGreenStepReporter
	if len(report) > 0 {
		notify = report[0]
	}
	reportStep := func(step, summary string) {
		if notify != nil {
			notify(step, summary)
		}
	}
	activeSlot, candidateSlot := ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
	activeName := serviceName + "-" + activeSlot
	candidateName := serviceName + "-" + candidateSlot
	namespace := shared.ResolveNamespace(cfg, environment)

	kubeClient, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return "", err
	}

	if logger != nil {
		logger.Logf(ctx, "blue-green waiting for %s before moving traffic off %s", candidateName, activeName)
	}
	reportStep("ready", fmt.Sprintf("Waiting until %s is fully live", candidateSlot))
	if err := waitForServiceDeployReadiness(
		ctx,
		cfg,
		environment,
		namespace,
		serviceName,
		[]string{candidateName},
		service,
		logger,
	); err != nil {
		return "", fmt.Errorf("blue-green candidate is not ready, active traffic was kept: %w", err)
	}
	if err := waitForDeploymentRollout(ctx, kubeClient, kubeToken, namespace, candidateName); err != nil {
		return "", fmt.Errorf("blue-green candidate is not fully live, active traffic was kept: %w", err)
	}

	if logger != nil {
		logger.Logf(ctx, "blue-green promoting traffic from %s to %s", activeName, candidateName)
	}
	reportStep("shift", fmt.Sprintf("Moving traffic from %s to %s", activeSlot, candidateSlot))

	if err := switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, candidateName); err != nil {
		return "", err
	}

	if err := platformops.UpdateBlueGreenActiveSlot(ctx, client, cfg, tokens, serviceID, environment, candidateSlot); err != nil {
		_ = switchBlueGreenCanonicalService(ctx, kubeClient, kubeToken, namespace, serviceName, activeName)
		return "", MarkRollbackPerformed(err)
	}

	observedService := service
	observedService.DeploymentStrategy.BlueGreenPrimary = candidateSlot
	reportStep("observe", fmt.Sprintf("Watching %s before removing %s", candidateSlot, activeSlot))
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
		return "", MarkRollbackPerformed(fmt.Errorf("blue-green promoted slot unhealthy: %w", err))
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

	reportStep("cleanup", fmt.Sprintf("Removing the previous %s slot", activeSlot))
	if err := cleanupBlueGreenSlot(ctx, kubeClient, kubeToken, namespace, activeName, logger); err != nil {
		return "", fmt.Errorf("previous blue-green slot did not delete: %w", err)
	}
	if err := cleanupLegacyRollingWorkload(ctx, kubeClient, kubeToken, namespace, serviceName, logger); err != nil {
		return "", fmt.Errorf("previous workload did not delete: %w", err)
	}
	reportStep("finalize", fmt.Sprintf("Waiting until %s has finished shutting down", activeSlot))
	if err := waitForRetiredWorkloads(ctx, kubeClient, kubeToken, namespace, []string{activeName}); err != nil {
		return "", err
	}
	if logger != nil {
		logger.Logf(ctx, "blue-green previous slot %s has finished and %s is fully live", activeName, candidateName)
		logger.Flush(ctx)
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
