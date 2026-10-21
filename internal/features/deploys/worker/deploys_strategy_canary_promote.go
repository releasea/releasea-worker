package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
	"time"
)

func HandlePromoteCanary(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
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
	if ResolveDeployStrategyType(contextData.Service) != "canary" {
		return fmt.Errorf("service is not using canary strategy")
	}

	serviceName := shared.ToKubeName(contextData.Service.Name)
	if serviceName == "" {
		serviceName = shared.ToKubeName(contextData.Service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}
	namespace := shared.ResolveNamespace(cfg, environment)
	kubeClient, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return err
	}

	canaryName := serviceName + "-canary"
	canaryDeployment, err := platformkube.FetchResourceAsMap(ctx, kubeClient, kubeToken, "apps/v1", "Deployment", namespace, canaryName)
	if err != nil {
		return fmt.Errorf("canary deployment not found (promote only after canary deploy): %w", err)
	}
	canaryService, err := platformkube.FetchResourceAsMap(ctx, kubeClient, kubeToken, "v1", "Service", namespace, canaryName)
	if err != nil {
		return fmt.Errorf("canary service not found: %w", err)
	}

	desiredReplicas := resolveDesiredReplicasForPromote(contextData.Service)
	stableDeployment := shared.RenderTemplateResource(canaryDeployment, map[string]string{})
	stableService := shared.RenderTemplateResource(canaryService, map[string]string{})
	if stableDeployment == nil || stableService == nil {
		return errors.New("failed to clone canary resources")
	}
	platformkube.CleanResourceForReapply(stableDeployment)
	platformkube.CleanResourceForReapply(stableService)
	RetargetDeployment(stableDeployment, serviceName, serviceName, desiredReplicas)
	setZeroDowntimeRollingStrategy(stableDeployment)
	stampDeploymentPromotion(stableDeployment)
	RetargetService(stableService, serviceName, serviceName)

	log.Printf("[worker] promote canary: applying stable %s (replicas=%d)", serviceName, desiredReplicas)
	reportCanaryPromotion(ctx, client, cfg, tokens, op.DeployID, "prepare", "Copying the canary onto the stable deployment")
	if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, stableDeployment); err != nil {
		return fmt.Errorf("apply stable deployment: %w", err)
	}
	reportCanaryPromotion(ctx, client, cfg, tokens, op.DeployID, "ready", "Waiting until the new stable pods are ready")
	if err := waitForDeploymentRollout(ctx, kubeClient, kubeToken, namespace, serviceName); err != nil {
		return fmt.Errorf("stable target not ready after canary promote: %w", err)
	}
	reportCanaryPromotion(ctx, client, cfg, tokens, op.DeployID, "shift", "Moving published traffic onto the stable version")
	if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, stableService); err != nil {
		return fmt.Errorf("apply stable service: %w", err)
	}
	if err := shiftPublishedCanaryTraffic(ctx, kubeClient, kubeToken, namespace, serviceName); err != nil {
		return err
	}
	if stillCanary, err := canaryTrafficRemains(ctx, kubeClient, kubeToken, namespace, serviceName); err != nil {
		return err
	} else if stillCanary {
		return errors.New("published traffic still references the canary")
	}

	reportCanaryPromotion(ctx, client, cfg, tokens, op.DeployID, "cleanup", "Removing the canary workload")
	if err := cleanupUnusedStrategyWorkloads(ctx, kubeClient, kubeToken, namespace, serviceName, "rolling", nil); err != nil {
		return fmt.Errorf("remove previous canary workload: %w", err)
	}
	reportCanaryPromotion(ctx, client, cfg, tokens, op.DeployID, "finalize", "Waiting until the previous deployment has finished")
	if err := waitForRetiredWorkloads(ctx, kubeClient, kubeToken, namespace, []string{canaryName}); err != nil {
		return fmt.Errorf("previous canary did not finish shutting down: %w", err)
	}
	log.Printf("[worker] promote canary: completed for service=%s env=%s", op.Resource, environment)
	return nil
}

func canaryTrafficRemains(ctx context.Context, client *http.Client, token, namespace, serviceName string) (bool, error) {
	items, err := listVirtualServiceItems(ctx, client, token, namespace)
	if err != nil {
		return false, err
	}
	canaryName := serviceName + "-canary"
	for _, vs := range items {
		if virtualServiceReferencesWorkload(vs, canaryName, namespace) {
			return true, nil
		}
	}
	return false, nil
}

func virtualServiceReferencesWorkload(vs map[string]interface{}, workloadName, namespace string) bool {
	spec := shared.MapValue(vs["spec"])
	for _, key := range []string{"http", "tcp", "tls"} {
		routes, ok := spec[key].([]interface{})
		if !ok {
			continue
		}
		for _, raw := range routes {
			route := shared.MapValue(raw)
			if hostMatchesWorkloadAlias(shared.StringValue(shared.MapValue(route["mirror"]), "host"), workloadName, namespace) {
				return true
			}
			entries, ok := route["route"].([]interface{})
			if !ok {
				continue
			}
			for _, entry := range entries {
				dest := shared.MapValue(shared.MapValue(entry)["destination"])
				if hostMatchesWorkloadAlias(shared.StringValue(dest, "host"), workloadName, namespace) {
					return true
				}
			}
		}
	}
	return false
}

func resolveDesiredReplicasForPromote(s models.ServiceConfig) int {
	minReplicas := s.MinReplicas
	if minReplicas < 1 {
		minReplicas = 1
	}
	if s.Replicas > 0 {
		minReplicas = s.Replicas
	}
	if s.MaxReplicas > 0 && minReplicas > s.MaxReplicas {
		return s.MaxReplicas
	}
	return minReplicas
}

func reportCanaryPromotion(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, deployID, step, summary string) {
	if strings.TrimSpace(deployID) == "" {
		return
	}
	if err := platformops.UpdateDeployStrategyStatus(ctx, client, cfg, tokens, deployID, "", "canary", "promoting", summary, map[string]interface{}{"step": step}); err != nil {
		log.Printf("[worker] promote canary step %s: %v", step, err)
	}
}

func shiftPublishedCanaryTraffic(ctx context.Context, client *http.Client, token, namespace, serviceName string) error {
	items, err := listVirtualServiceItems(ctx, client, token, namespace)
	if err != nil {
		return fmt.Errorf("list virtual services before canary promote: %w", err)
	}
	for _, vs := range items {
		if !promoteCanaryVirtualService(vs, serviceName, namespace) {
			continue
		}
		platformkube.CleanResourceForReapply(vs)
		meta := shared.MapValue(vs["metadata"])
		name := shared.StringValue(meta, "name")
		if err := platformkube.ApplyResource(ctx, client, token, vs); err != nil {
			return fmt.Errorf("shift virtual service %s off canary: %w", name, err)
		}
		log.Printf("[worker] promote canary: shifted virtual service %s to stable", name)
	}
	return nil
}

func listVirtualServiceItems(ctx context.Context, client *http.Client, token, namespace string) ([]map[string]interface{}, error) {
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
		return nil, nil
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
	return payload.Items, nil
}

func promoteCanaryVirtualService(vs map[string]interface{}, serviceName, namespace string) bool {
	spec := shared.MapValue(vs["spec"])
	if len(spec) == 0 {
		return false
	}
	changed := false
	for _, key := range []string{"http", "tcp", "tls"} {
		routes, ok := spec[key].([]interface{})
		if !ok {
			continue
		}
		for i, raw := range routes {
			route := shared.MapValue(raw)
			if len(route) == 0 {
				continue
			}
			if clearCanaryMirror(route, serviceName, namespace) {
				changed = true
			}
			next, did := shiftWeightedRoute(route["route"], serviceName, namespace)
			if did {
				route["route"] = next
				changed = true
			}
			routes[i] = route
		}
		spec[key] = routes
	}
	vs["spec"] = spec
	return changed
}

func clearCanaryMirror(route map[string]interface{}, serviceName, namespace string) bool {
	mirror := shared.MapValue(route["mirror"])
	host := shared.StringValue(mirror, "host")
	if !hostMatchesWorkloadAlias(host, serviceName+"-canary", namespace) {
		return false
	}
	delete(route, "mirror")
	delete(route, "mirrorPercentage")
	return true
}

func shiftWeightedRoute(raw interface{}, serviceName, namespace string) ([]interface{}, bool) {
	entries, ok := raw.([]interface{})
	if !ok || len(entries) == 0 {
		return nil, false
	}
	canaryName := serviceName + "-canary"
	stableHost := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
	changed := false
	merged := make([]map[string]interface{}, 0, len(entries))
	indexByHost := map[string]int{}
	for _, entry := range entries {
		route := shared.MapValue(entry)
		if len(route) == 0 {
			continue
		}
		dest := shared.MapValue(route["destination"])
		host := shared.StringValue(dest, "host")
		if hostMatchesWorkloadAlias(host, canaryName, namespace) {
			host = stableHost
			dest["host"] = stableHost
			delete(dest, "subset")
			route["destination"] = dest
			changed = true
		}
		if host == "" {
			merged = append(merged, route)
			continue
		}
		weight := routeWeight(route["weight"])
		if existing, found := indexByHost[host]; found {
			previous := merged[existing]
			previous["weight"] = routeWeight(previous["weight"]) + weight
			continue
		}
		route["weight"] = weight
		indexByHost[host] = len(merged)
		merged = append(merged, route)
	}
	if !changed {
		return entries, false
	}
	if len(merged) == 1 {
		merged[0]["weight"] = 100
	}
	out := make([]interface{}, 0, len(merged))
	for _, route := range merged {
		out = append(out, route)
	}
	return out, true
}

func routeWeight(value interface{}) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return 0
		}
		return int(parsed)
	default:
		return 0
	}
}

func stampDeploymentPromotion(resource map[string]interface{}) {
	spec := shared.MapValue(resource["spec"])
	template := shared.MapValue(spec["template"])
	templateMeta := shared.MapValue(template["metadata"])
	annotations := shared.MapValue(templateMeta["annotations"])
	annotations["releasea.promoted-at"] = time.Now().UTC().Format(time.RFC3339)
	templateMeta["annotations"] = annotations
	template["metadata"] = templateMeta
	spec["template"] = template
	resource["spec"] = spec
}
