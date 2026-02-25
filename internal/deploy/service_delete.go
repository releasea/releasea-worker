package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"releaseaworker/internal/shared"
	"strings"
)

func HandleServiceDelete(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, op models.OperationPayload) error {
	environment := platform.PayloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}

	ctxData, err := fetchServiceContext(ctx, client, cfg, tokens, op.Resource, environment)
	if err != nil {
		return err
	}

	serviceName := shared.ToKubeName(ctxData.Service.Name)
	if serviceName == "" {
		serviceName = shared.ToKubeName(op.ServiceName)
	}
	if serviceName == "" {
		serviceName = shared.ToKubeName(op.Resource)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}

	namespace := shared.ResolveNamespace(cfg, environment)
	if err := shared.ValidateAppNamespace(namespace); err != nil {
		return fmt.Errorf("service delete blocked: %w", err)
	}

	kubeClient, token, err := platform.KubeClient()
	if err != nil {
		return err
	}

	workloadNames := []string{serviceName, serviceName + "-canary", serviceName + "-blue", serviceName + "-green"}
	for _, name := range workloadNames {
		if err := platform.DeleteResource(ctx, kubeClient, token, "apps/v1", "Deployment", namespace, name); err != nil {
			return err
		}
		if err := platform.DeleteResource(ctx, kubeClient, token, "v1", "Service", namespace, name); err != nil {
			return err
		}
	}
	if err := platform.DeleteResource(ctx, kubeClient, token, "batch/v1", "CronJob", namespace, serviceName); err != nil {
		return err
	}
	if err := platform.DeleteResourcesByLabel(ctx, kubeClient, token, "batch/v1", "Job", namespace, fmt.Sprintf("app=%s", serviceName)); err != nil {
		return err
	}
	if err := platform.DeleteResource(ctx, kubeClient, token, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName); err != nil {
		return err
	}
	if err := platform.DeleteResource(ctx, kubeClient, token, "v1", "Secret", namespace, serviceName); err != nil {
		return err
	}
	if err := platform.DeleteResource(ctx, kubeClient, token, "v1", "ConfigMap", namespace, serviceName); err != nil {
		return err
	}
	if err := platform.DeleteResourcesByLabel(ctx, kubeClient, token, "networking.istio.io/v1beta1", "VirtualService", namespace, fmt.Sprintf("releasea.service=%s", ctxData.Service.ID)); err != nil {
		return err
	}
	if err := platform.DeleteResourcesByLabel(ctx, kubeClient, token, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, fmt.Sprintf("releasea.service=%s", ctxData.Service.ID)); err != nil {
		return err
	}

	if strings.EqualFold(ctxData.Service.Type, "static-site") {
		if err := deleteStaticSiteAssets(ctx, cfg, ctxData.Service); err != nil {
			return err
		}
	}

	if err := deleteManagedRepository(ctx, client, ctxData.SCM, ctxData.Service); err != nil {
		return err
	}

	return nil
}

func fetchServiceContext(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager, serviceID, environment string) (models.DeployContext, error) {
	if serviceID == "" {
		return models.DeployContext{}, errors.New("service id missing")
	}
	payload := map[string]string{
		"serviceId":   serviceID,
		"environment": environment,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return models.DeployContext{}, err
	}
	token, err := tokens.Get(ctx, client, cfg)
	if err != nil {
		return models.DeployContext{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ApiBaseURL+"/workers/credentials", bytes.NewReader(body))
	if err != nil {
		return models.DeployContext{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	platform.SetAuthHeaders(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return models.DeployContext{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		tokens.Invalidate()
		return models.DeployContext{}, fmt.Errorf("credentials fetch unauthorized")
	}
	if resp.StatusCode >= 400 {
		return models.DeployContext{}, fmt.Errorf("credentials fetch failed: %s", resp.Status)
	}
	var contextData models.DeployContext
	if err := json.NewDecoder(resp.Body).Decode(&contextData); err != nil {
		return models.DeployContext{}, err
	}
	return contextData, nil
}
