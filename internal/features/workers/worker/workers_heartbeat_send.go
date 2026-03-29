package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"sync"
)

type heartbeatDeltaState struct {
	mu                          sync.Mutex
	lastDiscoveredWorkloadsHash string
}

var defaultHeartbeatDeltaState = &heartbeatDeltaState{}

func sendHeartbeat(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager) error {
	payload, err := buildHeartbeatPayload(ctx, cfg, defaultHeartbeatDeltaState)
	if err != nil {
		return err
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

func buildHeartbeatPayload(ctx context.Context, cfg models.Config, deltaState *heartbeatDeltaState) (map[string]interface{}, error) {
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
	if workloads, err := platformkube.DiscoverWorkloads(ctx, cfg); err == nil {
		hash, includeWorkloads, mode, err := nextDiscoveredWorkloadHeartbeat(workloads, deltaState)
		if err != nil {
			return nil, err
		}
		payload["discoveredWorkloadsHash"] = hash
		payload["discoveredWorkloadsMode"] = mode
		if includeWorkloads {
			payload["discoveredWorkloads"] = workloads
		}
	}

	return payload, nil
}

func nextDiscoveredWorkloadHeartbeat(workloads []models.DiscoveredWorkload, deltaState *heartbeatDeltaState) (hash string, includeWorkloads bool, mode string, err error) {
	hash, err = fingerprintDiscoveredWorkloads(workloads)
	if err != nil {
		return "", false, "", err
	}

	deltaState.mu.Lock()
	defer deltaState.mu.Unlock()

	if deltaState.lastDiscoveredWorkloadsHash == "" || deltaState.lastDiscoveredWorkloadsHash != hash {
		deltaState.lastDiscoveredWorkloadsHash = hash
		return hash, true, "full", nil
	}
	return hash, false, "unchanged", nil
}

func fingerprintDiscoveredWorkloads(workloads []models.DiscoveredWorkload) (string, error) {
	body, err := json.Marshal(workloads)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
