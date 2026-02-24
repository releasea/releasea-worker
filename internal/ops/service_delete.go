package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func handleServiceDelete(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	environment := payloadString(op.Payload, "environment")
	if environment == "" {
		environment = "prod"
	}

	ctxData, err := fetchServiceContext(ctx, client, cfg, tokens, op.Resource, environment)
	if err != nil {
		return err
	}

	serviceName := toKubeName(ctxData.Service.Name)
	if serviceName == "" {
		serviceName = toKubeName(op.ServiceName)
	}
	if serviceName == "" {
		serviceName = toKubeName(op.Resource)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}

	namespace := resolveNamespace(cfg, environment)
	if err := validateAppNamespace(namespace); err != nil {
		return fmt.Errorf("service delete blocked: %w", err)
	}

	kubeClient, token, err := kubeClient()
	if err != nil {
		return err
	}

	workloadNames := []string{serviceName, serviceName + "-canary", serviceName + "-blue", serviceName + "-green"}
	for _, name := range workloadNames {
		if err := deleteResource(ctx, kubeClient, token, "apps/v1", "Deployment", namespace, name); err != nil {
			return err
		}
		if err := deleteResource(ctx, kubeClient, token, "v1", "Service", namespace, name); err != nil {
			return err
		}
	}
	if err := deleteResource(ctx, kubeClient, token, "batch/v1", "CronJob", namespace, serviceName); err != nil {
		return err
	}
	if err := deleteResourcesByLabel(ctx, kubeClient, token, "batch/v1", "Job", namespace, fmt.Sprintf("app=%s", serviceName)); err != nil {
		return err
	}
	if err := deleteResource(ctx, kubeClient, token, "autoscaling/v2", "HorizontalPodAutoscaler", namespace, serviceName); err != nil {
		return err
	}
	if err := deleteResource(ctx, kubeClient, token, "v1", "Secret", namespace, serviceName); err != nil {
		return err
	}
	if err := deleteResource(ctx, kubeClient, token, "v1", "ConfigMap", namespace, serviceName); err != nil {
		return err
	}
	if err := deleteResourcesByLabel(ctx, kubeClient, token, "networking.istio.io/v1beta1", "VirtualService", namespace, fmt.Sprintf("releasea.service=%s", ctxData.Service.ID)); err != nil {
		return err
	}
	if err := deleteResourcesByLabel(ctx, kubeClient, token, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, fmt.Sprintf("releasea.service=%s", ctxData.Service.ID)); err != nil {
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

func fetchServiceContext(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, serviceID, environment string) (deployContext, error) {
	if serviceID == "" {
		return deployContext{}, errors.New("service id missing")
	}
	payload := map[string]string{
		"serviceId":   serviceID,
		"environment": environment,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return deployContext{}, err
	}
	token, err := tokens.get(ctx, client, cfg)
	if err != nil {
		return deployContext{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.ApiBaseURL+"/workers/credentials", bytes.NewReader(body))
	if err != nil {
		return deployContext{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	setAuthHeaders(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return deployContext{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		tokens.invalidate()
		return deployContext{}, fmt.Errorf("credentials fetch unauthorized")
	}
	if resp.StatusCode >= 400 {
		return deployContext{}, fmt.Errorf("credentials fetch failed: %s", resp.Status)
	}
	var contextData deployContext
	if err := json.NewDecoder(resp.Body).Decode(&contextData); err != nil {
		return deployContext{}, err
	}
	return contextData, nil
}
