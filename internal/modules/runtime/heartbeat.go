package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"releaseaworker/internal/models"
	"releaseaworker/internal/modules/platform"
)

func sendHeartbeat(ctx context.Context, client *http.Client, cfg models.Config, tokens *platform.TokenManager) error {
	desiredAgents := platform.GetDesiredAgents(ctx, cfg)

	payload := map[string]interface{}{
		"id":              cfg.WorkerID,
		"name":            cfg.WorkerName,
		"environment":     cfg.Environment,
		"namespace":       cfg.Namespace,
		"namespacePrefix": cfg.NamespacePrefix,
		"cluster":         cfg.Cluster,
		"version":         cfg.Version,
		"status":          "online",
	}
	if cfg.DeploymentName != "" {
		payload["deploymentName"] = cfg.DeploymentName
	}
	if cfg.DeploymentNamespace != "" {
		payload["deploymentNamespace"] = cfg.DeploymentNamespace
	}
	if desiredAgents > 0 {
		payload["desiredAgents"] = desiredAgents
	}
	if len(cfg.Tags) > 0 {
		payload["tags"] = cfg.Tags
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return platform.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodPost,
		cfg.ApiBaseURL+"/workers/heartbeat",
		body,
		nil,
		"heartbeat",
	)
}
