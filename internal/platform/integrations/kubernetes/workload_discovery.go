package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"sort"
	"strconv"
	"strings"
)

type kubernetesListResponse struct {
	Items []map[string]interface{} `json:"items"`
}

type discoveryTarget struct {
	kind string
	url  string
}

func DiscoverWorkloads(ctx context.Context, cfg models.Config) ([]models.DiscoveredWorkload, error) {
	client, token, err := KubeClient()
	if err != nil {
		return nil, err
	}

	environment := strings.TrimSpace(cfg.Environment)
	if environment == "" {
		environment = "prod"
	}
	namespace := shared.ResolveNamespace(cfg, environment)
	baseURL := KubeAPIBaseURL()
	targets := []discoveryTarget{
		{kind: "Deployment", url: fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments", baseURL, namespace)},
		{kind: "StatefulSet", url: fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/statefulsets", baseURL, namespace)},
		{kind: "CronJob", url: fmt.Sprintf("%s/apis/batch/v1/namespaces/%s/cronjobs", baseURL, namespace)},
	}

	discovered := make([]models.DiscoveredWorkload, 0, 16)
	for _, target := range targets {
		items, err := listDiscoveryItems(ctx, client, token, target.url)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			workload, ok := mapDiscoveredWorkload(target.kind, namespace, item)
			if !ok {
				continue
			}
			discovered = append(discovered, workload)
		}
	}

	sort.Slice(discovered, func(i, j int) bool {
		if discovered[i].Kind != discovered[j].Kind {
			return discovered[i].Kind < discovered[j].Kind
		}
		return discovered[i].Name < discovered[j].Name
	})
	return discovered, nil
}

func listDiscoveryItems(ctx context.Context, client *http.Client, token, endpoint string) ([]map[string]interface{}, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("workload discovery failed: %s", resp.Status)
	}
	var payload kubernetesListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Items, nil
}

func mapDiscoveredWorkload(kind, namespace string, resource map[string]interface{}) (models.DiscoveredWorkload, bool) {
	metadata := shared.MapValue(resource["metadata"])
	if isReleaseaManagedWorkload(metadata) {
		return models.DiscoveredWorkload{}, false
	}
	name := strings.TrimSpace(shared.StringValue(metadata, "name"))
	if name == "" {
		return models.DiscoveredWorkload{}, false
	}

	spec := shared.MapValue(resource["spec"])
	template := shared.MapValue(spec["template"])
	if kind == "CronJob" {
		template = shared.MapValue(shared.MapValue(spec["jobTemplate"])["spec"])
		template = shared.MapValue(template["template"])
	}
	podSpec := shared.MapValue(template["spec"])
	containers := interfaceSlice(podSpec["containers"])
	images := uniqueStrings(extractContainerImages(containers))
	ports := uniquePositiveInts(extractContainerPorts(containers))
	primaryImage := ""
	if len(images) > 0 {
		primaryImage = images[0]
	}
	port := 0
	if len(ports) > 0 {
		port = ports[0]
	}

	workload := models.DiscoveredWorkload{
		Kind:            kind,
		Name:            name,
		Namespace:       namespace,
		Images:          images,
		PrimaryImage:    primaryImage,
		Ports:           ports,
		Port:            port,
		HealthCheckPath: extractHealthCheckPath(containers),
	}
	switch kind {
	case "CronJob":
		workload.ScheduleCron = strings.TrimSpace(shared.StringValue(spec, "schedule"))
	default:
		replicas := intValue(spec, "replicas")
		if replicas <= 0 {
			replicas = 1
		}
		workload.Replicas = replicas
	}
	return workload, true
}

func isReleaseaManagedWorkload(metadata map[string]interface{}) bool {
	labels := shared.MapValue(metadata["labels"])
	if strings.TrimSpace(shared.StringValue(labels, "releasea.service")) != "" {
		return true
	}
	annotations := shared.MapValue(metadata["annotations"])
	return strings.TrimSpace(shared.StringValue(annotations, "releasea.io/deploy-revision")) != ""
}

func extractContainerImages(containers []interface{}) []string {
	images := make([]string, 0, len(containers))
	for _, raw := range containers {
		container := shared.MapValue(raw)
		image := strings.TrimSpace(shared.StringValue(container, "image"))
		if image != "" {
			images = append(images, image)
		}
	}
	return images
}

func extractContainerPorts(containers []interface{}) []int {
	ports := make([]int, 0, len(containers))
	for _, raw := range containers {
		container := shared.MapValue(raw)
		for _, portRaw := range interfaceSlice(container["ports"]) {
			portSpec := shared.MapValue(portRaw)
			port := intValue(portSpec, "containerPort")
			if port > 0 {
				ports = append(ports, port)
			}
		}
	}
	return ports
}

func extractHealthCheckPath(containers []interface{}) string {
	for _, raw := range containers {
		container := shared.MapValue(raw)
		for _, probeKey := range []string{"readinessProbe", "livenessProbe", "startupProbe"} {
			probe := shared.MapValue(container[probeKey])
			httpGet := shared.MapValue(probe["httpGet"])
			path := strings.TrimSpace(shared.StringValue(httpGet, "path"))
			if path != "" {
				return path
			}
		}
	}
	return ""
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func uniquePositiveInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(values))
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

func interfaceSlice(value interface{}) []interface{} {
	if value == nil {
		return nil
	}
	if typed, ok := value.([]interface{}); ok {
		return typed
	}
	return nil
}

func intValue(source map[string]interface{}, key string) int {
	if source == nil {
		return 0
	}
	raw, ok := source[key]
	if !ok || raw == nil {
		return 0
	}
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		parsed, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
		return parsed
	}
}
