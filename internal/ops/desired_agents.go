package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

type replicaCache struct {
	value     int
	updatedAt time.Time
	mu        sync.Mutex
}

var desiredAgentsCache replicaCache

func getDesiredAgents(ctx context.Context, cfg Config) int {
	desiredAgentsCache.mu.Lock()
	if time.Since(desiredAgentsCache.updatedAt) < 20*time.Second && desiredAgentsCache.value > 0 {
		value := desiredAgentsCache.value
		desiredAgentsCache.mu.Unlock()
		return value
	}
	desiredAgentsCache.mu.Unlock()

	desired, err := fetchDeploymentReplicas(ctx, cfg)
	if err != nil || desired == 0 {
		if cfg.DesiredAgentsFallback > 0 {
			desired = cfg.DesiredAgentsFallback
		}
	}
	if desired > 0 {
		desiredAgentsCache.mu.Lock()
		desiredAgentsCache.value = desired
		desiredAgentsCache.updatedAt = time.Now()
		desiredAgentsCache.mu.Unlock()
	}
	return desired
}

func fetchDeploymentReplicas(ctx context.Context, cfg Config) (int, error) {
	if cfg.DeploymentName == "" || cfg.DeploymentNamespace == "" {
		return 0, errors.New("deployment metadata missing")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		return 0, errors.New("not running in cluster")
	}

	client, token, err := kubeClient()
	if err != nil {
		return 0, err
	}
	url := fmt.Sprintf("https://kubernetes.default.svc/apis/apps/v1/namespaces/%s/deployments/%s", cfg.DeploymentNamespace, cfg.DeploymentName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("kubernetes api error: %s", resp.Status)
	}

	var body struct {
		Spec struct {
			Replicas *int `json:"replicas"`
		} `json:"spec"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, err
	}
	if body.Spec.Replicas == nil {
		return 0, errors.New("replicas not set")
	}
	return *body.Spec.Replicas, nil
}
