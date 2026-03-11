package workers

import (
	"context"
	"encoding/json"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
)

func sendHeartbeat(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager) error {
	desiredAgents := platformkube.GetDesiredAgents(ctx, cfg)

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
	if cfg.BootstrapProfileVersion != "" {
		payload["bootstrapProfileVersion"] = cfg.BootstrapProfileVersion
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

	return platformops.DoJSONRequest(
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
