package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func restartDeployment(ctx context.Context, cfg Config, payload map[string]interface{}) error {
	deploymentName := payloadString(payload, "deploymentName")
	deploymentNamespace := payloadString(payload, "deploymentNamespace")
	if deploymentName == "" {
		deploymentName = cfg.DeploymentName
	}
	if deploymentNamespace == "" {
		deploymentNamespace = cfg.DeploymentNamespace
	}
	if deploymentName == "" || deploymentNamespace == "" {
		return errors.New("deployment metadata missing")
	}
	return restartDeploymentByName(ctx, deploymentNamespace, deploymentName)
}

func restartDeploymentByName(ctx context.Context, deploymentNamespace, deploymentName string) error {
	if deploymentName == "" || deploymentNamespace == "" {
		return errors.New("deployment metadata missing")
	}

	client, token, err := kubeClient()
	if err != nil {
		return err
	}

	restartedAt := time.Now().UTC().Format(time.RFC3339)
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"annotations": map[string]string{
						"releasea.dev/restartedAt": restartedAt,
					},
				},
			},
		},
	}
	resp, err := patchDeployment(ctx, client, token, deploymentNamespace, deploymentName, patch, "application/merge-patch+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("kubernetes api error: %s", resp.Status)
	}
	return nil
}

func scaleDeployment(ctx context.Context, deploymentNamespace, deploymentName string, replicas int) error {
	if deploymentName == "" || deploymentNamespace == "" {
		return errors.New("deployment metadata missing")
	}
	if replicas < 0 {
		replicas = 0
	}

	client, token, err := kubeClient()
	if err != nil {
		return err
	}

	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"replicas": replicas,
		},
	}
	resp, err := patchDeployment(ctx, client, token, deploymentNamespace, deploymentName, patch, "application/merge-patch+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errDeploymentNotFound
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("kubernetes api error: %s", resp.Status)
	}
	return nil
}

func patchDeployment(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	name string,
	patch map[string]interface{},
	contentType string,
) (*http.Response, error) {
	body, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://kubernetes.default.svc/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	return client.Do(req)
}
