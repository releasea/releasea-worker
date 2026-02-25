package rules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
)

var errDeploymentNotFound = errors.New("deployment not found")

type strategyCleanupLogger interface {
	Logf(ctx context.Context, format string, args ...interface{})
	Flush(ctx context.Context)
}

func normalizeRuleDeployStrategyForAvailability(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	service models.ServicePayload,
	logger strategyCleanupLogger,
) models.ServicePayload {
	strategyType := resolveDeployStrategyTypeForServicePayload(service)
	switch strategyType {
	case "canary":
		if service.DeploymentStrategy.CanaryPercent <= 0 {
			return service
		}
		stableReady, err := workloadTargetReady(ctx, client, token, namespace, serviceName)
		if err != nil {
			return service
		}
		canaryName := serviceName + "-canary"
		canaryReady, err := workloadTargetReady(ctx, client, token, namespace, canaryName)
		if err != nil {
			return service
		}
		if stableReady && canaryReady {
			return service
		}
		// Keep availability first: if either side is missing, route only to stable.
		service.DeploymentStrategy.CanaryPercent = 0
		if logger != nil {
			logger.Logf(ctx, "canary target not ready, routing 100%% to stable")
		}
		return service
	case "blue-green":
		primaryColor, secondaryColor := shared.ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
		primaryName := serviceName + "-" + primaryColor
		secondaryName := serviceName + "-" + secondaryColor

		primaryReady, err := workloadTargetReady(ctx, client, token, namespace, primaryName)
		if err != nil {
			return service
		}
		if primaryReady {
			return service
		}

		secondaryReady, err := workloadTargetReady(ctx, client, token, namespace, secondaryName)
		if err != nil {
			return service
		}
		if secondaryReady {
			service.DeploymentStrategy.BlueGreenPrimary = secondaryColor
			if logger != nil {
				logger.Logf(ctx, "blue-green primary unavailable, temporary fallback to %s", secondaryColor)
			}
			return service
		}

		canonicalServiceExists, err := platformkube.ResourceExists(ctx, client, token, "v1", "Service", namespace, serviceName)
		if err != nil || !canonicalServiceExists {
			return service
		}

		// If neither slot is ready, keep canonical routing without forcing blue/green destinations.
		service.DeploymentStrategy.Type = "rolling"
		service.DeploymentStrategy.BlueGreenPrimary = ""
		if logger != nil {
			logger.Logf(ctx, "blue-green slots unavailable, temporary fallback to canonical routing")
		}
		return service
	default:
		return service
	}
}

func cleanupUnusedStrategyWorkloads(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	strategyType string,
	logger strategyCleanupLogger,
) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil
	}

	candidates := cleanupCandidatesForStrategy(serviceName, strategyType)
	if len(candidates) == 0 {
		return nil
	}

	hostsInUse, err := listVirtualServiceDestinationHosts(ctx, client, token, namespace)
	if err != nil {
		return err
	}
	canonicalTarget, err := canonicalServiceTarget(ctx, client, token, namespace, serviceName)
	if err != nil {
		return err
	}

	for _, candidate := range candidates {
		if candidate == canonicalTarget {
			if logger != nil {
				logger.Logf(ctx, "keeping %s while canonical service still targets it", candidate)
			}
			continue
		}
		if isWorkloadAliasReferenced(hostsInUse, candidate, namespace) {
			if logger != nil {
				logger.Logf(ctx, "keeping %s while traffic still references it", candidate)
			}
			continue
		}
		if err := platformkube.DeleteResource(ctx, client, token, "apps/v1", "Deployment", namespace, candidate); err != nil {
			return err
		}
		if err := platformkube.DeleteResource(ctx, client, token, "v1", "Service", namespace, candidate); err != nil {
			return err
		}
		if logger != nil {
			logger.Logf(ctx, "cleanup shadow workload %s", candidate)
		}
	}
	if logger != nil {
		logger.Flush(ctx)
	}
	return nil
}

func workloadTargetReady(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	workloadName string,
) (bool, error) {
	serviceReady, err := platformkube.ResourceExists(ctx, client, token, "v1", "Service", namespace, workloadName)
	if err != nil || !serviceReady {
		return false, err
	}
	deploy, err := fetchDeployment(ctx, client, token, namespace, workloadName)
	if err != nil {
		if errors.Is(err, errDeploymentNotFound) {
			return false, nil
		}
		return false, err
	}
	return deploy.Status.AvailableReplicas > 0, nil
}

func fetchDeployment(ctx context.Context, client *http.Client, token, namespace, name string) (models.DeploymentInfo, error) {
	var deployment models.DeploymentInfo
	url := fmt.Sprintf("https://kubernetes.default.svc/apis/apps/v1/namespaces/%s/deployments/%s", namespace, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return deployment, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return deployment, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return deployment, errDeploymentNotFound
	}
	if resp.StatusCode >= 400 {
		return deployment, fmt.Errorf("kubernetes api error: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&deployment); err != nil {
		return deployment, err
	}
	return deployment, nil
}

func cleanupCandidatesForStrategy(serviceName string, strategyType string) []string {
	switch strings.ToLower(strings.TrimSpace(strategyType)) {
	case "rolling":
		return []string{serviceName + "-canary", serviceName + "-blue", serviceName + "-green"}
	case "canary":
		return []string{serviceName + "-blue", serviceName + "-green"}
	case "blue-green":
		return []string{serviceName + "-canary"}
	default:
		return nil
	}
}

func resolveDeployStrategyTypeForServicePayload(service models.ServicePayload) string {
	return shared.NormalizeType(service.DeployTemplateID, service.DeploymentStrategy.Type)
}

func listVirtualServiceDestinationHosts(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
) (map[string]struct{}, error) {
	_, listURL, err := platformkube.ResourceURLs("networking.istio.io/v1beta1", "VirtualService", namespace, "placeholder")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return map[string]struct{}{}, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("virtual service list failed: %s", resp.Status)
	}

	var payload struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	hosts := make(map[string]struct{})
	for _, vs := range payload.Items {
		collectDestinationHosts(vs, hosts)
	}
	return hosts, nil
}

func collectDestinationHosts(value interface{}, out map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, nested := range typed {
			if strings.EqualFold(strings.TrimSpace(key), "host") {
				if host, ok := nested.(string); ok {
					normalized := strings.ToLower(strings.TrimSpace(host))
					if normalized != "" {
						out[normalized] = struct{}{}
					}
				}
				continue
			}
			collectDestinationHosts(nested, out)
		}
	case []interface{}:
		for _, item := range typed {
			collectDestinationHosts(item, out)
		}
	}
}

func isWorkloadAliasReferenced(hosts map[string]struct{}, alias string, namespace string) bool {
	for host := range hosts {
		if hostMatchesWorkloadAlias(host, alias, namespace) {
			return true
		}
	}
	return false
}

func canonicalServiceTarget(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
) (string, error) {
	exists, err := platformkube.ResourceExists(ctx, client, token, "v1", "Service", namespace, serviceName)
	if err != nil || !exists {
		return "", err
	}
	canonicalService, err := platformkube.FetchResourceAsMap(ctx, client, token, "v1", "Service", namespace, serviceName)
	if err != nil {
		return "", err
	}
	spec := shared.MapValue(canonicalService["spec"])
	selector := shared.MapValue(spec["selector"])
	return strings.TrimSpace(shared.StringValue(selector, "app")), nil
}

func hostMatchesWorkloadAlias(host string, alias string, namespace string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	alias = strings.ToLower(strings.TrimSpace(alias))
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	if host == "" || alias == "" {
		return false
	}
	candidates := []string{
		alias,
		alias + "." + namespace,
		alias + "." + namespace + ".svc",
		alias + "." + namespace + ".svc.cluster.local",
	}
	for _, candidate := range candidates {
		if host == candidate {
			return true
		}
	}
	return false
}
