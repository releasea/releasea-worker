package operations

import (
	"context"
	"encoding/json"
	"net/http"
)

func sendHeartbeat(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager) error {
	desiredAgents := getDesiredAgents(ctx, cfg)

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

	return doJSONRequest(
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
