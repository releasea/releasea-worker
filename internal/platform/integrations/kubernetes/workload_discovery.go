package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	serviceItems, _ := listDiscoveryItems(ctx, client, token, fmt.Sprintf("%s/api/v1/namespaces/%s/services", baseURL, namespace))
	ingressItems, _ := listDiscoveryItems(ctx, client, token, fmt.Sprintf("%s/apis/networking.k8s.io/v1/namespaces/%s/ingresses", baseURL, namespace))

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
			podLabels := extractWorkloadPodLabels(target.kind, item)
			serviceHints := matchDiscoveredServices(serviceItems, podLabels)
			workload.ServiceHints = serviceHints
			workload.IngressHints = matchDiscoveredIngresses(ingressItems, serviceHints)
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
	primaryContainer := firstContainer(containers)
	probes := extractContainerProbes(primaryContainer)
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
		Kind:                 kind,
		Name:                 name,
		Namespace:            namespace,
		Containers:           extractDiscoveredContainers(containers),
		Images:               images,
		PrimaryImage:         primaryImage,
		Ports:                ports,
		Port:                 port,
		HealthCheckPath:      preferredProbeHealthCheckPath(probes),
		Probes:               probes,
		EnvironmentVariables: extractContainerEnvVars(primaryContainer),
		Command:              stringSliceValue(primaryContainer["command"]),
		Args:                 stringSliceValue(primaryContainer["args"]),
	}
	workload.CPUMilli, workload.MemoryMi = extractContainerResources(primaryContainer)
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

func firstContainer(containers []interface{}) map[string]interface{} {
	for _, raw := range containers {
		container := shared.MapValue(raw)
		if len(container) > 0 {
			return container
		}
	}
	return nil
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

func extractDiscoveredContainers(containers []interface{}) []models.DiscoveredContainer {
	if len(containers) == 0 {
		return nil
	}
	discovered := make([]models.DiscoveredContainer, 0, len(containers))
	importedAssigned := false
	for _, raw := range containers {
		container := shared.MapValue(raw)
		name := strings.TrimSpace(shared.StringValue(container, "name"))
		image := strings.TrimSpace(shared.StringValue(container, "image"))
		if name == "" && image == "" {
			continue
		}
		discovered = append(discovered, models.DiscoveredContainer{
			Name:     name,
			Image:    image,
			Ports:    uniquePositiveInts(extractContainerPorts([]interface{}{container})),
			Imported: !importedAssigned,
		})
		importedAssigned = true
	}
	if len(discovered) == 0 {
		return nil
	}
	return discovered
}

func extractWorkloadPodLabels(kind string, resource map[string]interface{}) map[string]string {
	spec := shared.MapValue(resource["spec"])
	template := shared.MapValue(spec["template"])
	if kind == "CronJob" {
		template = shared.MapValue(shared.MapValue(spec["jobTemplate"])["spec"])
		template = shared.MapValue(template["template"])
	}
	metadata := shared.MapValue(template["metadata"])
	labels := shared.MapValue(metadata["labels"])
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, raw := range labels {
		value := strings.TrimSpace(fmt.Sprint(raw))
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func matchDiscoveredServices(serviceItems []map[string]interface{}, podLabels map[string]string) []models.DiscoveredServiceHint {
	if len(serviceItems) == 0 || len(podLabels) == 0 {
		return nil
	}
	out := make([]models.DiscoveredServiceHint, 0, 4)
	for _, item := range serviceItems {
		metadata := shared.MapValue(item["metadata"])
		spec := shared.MapValue(item["spec"])
		name := strings.TrimSpace(shared.StringValue(metadata, "name"))
		if name == "" {
			continue
		}
		selector := shared.MapValue(spec["selector"])
		if len(selector) == 0 || !selectorMatchesLabels(selector, podLabels) {
			continue
		}
		out = append(out, models.DiscoveredServiceHint{
			Name:     name,
			Type:     strings.TrimSpace(shared.StringValue(spec, "type")),
			Ports:    extractServicePorts(spec),
			Headless: strings.EqualFold(strings.TrimSpace(shared.StringValue(spec, "clusterIP")), "None"),
		})
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func matchDiscoveredIngresses(ingressItems []map[string]interface{}, serviceHints []models.DiscoveredServiceHint) []models.DiscoveredIngressHint {
	if len(ingressItems) == 0 || len(serviceHints) == 0 {
		return nil
	}
	serviceNames := make(map[string]struct{}, len(serviceHints))
	for _, hint := range serviceHints {
		serviceNames[hint.Name] = struct{}{}
	}

	out := make([]models.DiscoveredIngressHint, 0, 4)
	for _, item := range ingressItems {
		metadata := shared.MapValue(item["metadata"])
		spec := shared.MapValue(item["spec"])
		name := strings.TrimSpace(shared.StringValue(metadata, "name"))
		if name == "" {
			continue
		}

		hosts := make([]string, 0, 4)
		paths := make([]string, 0, 8)
		matchedServices := make([]string, 0, 2)

		defaultBackend := shared.MapValue(spec["defaultBackend"])
		if backendServiceName := backendService(defaultBackend); backendServiceName != "" {
			if _, ok := serviceNames[backendServiceName]; ok {
				matchedServices = append(matchedServices, backendServiceName)
			}
		}

		for _, ruleRaw := range interfaceSlice(spec["rules"]) {
			rule := shared.MapValue(ruleRaw)
			host := strings.TrimSpace(shared.StringValue(rule, "host"))
			httpRule := shared.MapValue(rule["http"])
			for _, pathRaw := range interfaceSlice(httpRule["paths"]) {
				pathSpec := shared.MapValue(pathRaw)
				backendName := backendService(shared.MapValue(pathSpec["backend"]))
				if backendName == "" {
					continue
				}
				if _, ok := serviceNames[backendName]; !ok {
					continue
				}
				if host != "" {
					hosts = append(hosts, host)
				}
				pathValue := strings.TrimSpace(shared.StringValue(pathSpec, "path"))
				if pathValue != "" {
					paths = append(paths, pathValue)
				}
				matchedServices = append(matchedServices, backendName)
			}
		}

		matchedServices = uniqueStrings(matchedServices)
		if len(matchedServices) == 0 {
			continue
		}

		out = append(out, models.DiscoveredIngressHint{
			Name:         name,
			ServiceNames: matchedServices,
			Hosts:        uniqueStrings(hosts),
			Paths:        uniqueStrings(paths),
			TLS:          len(interfaceSlice(spec["tls"])) > 0,
		})
	}

	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func selectorMatchesLabels(selector map[string]interface{}, labels map[string]string) bool {
	if len(selector) == 0 || len(labels) == 0 {
		return false
	}
	for key, raw := range selector {
		expected := strings.TrimSpace(fmt.Sprint(raw))
		if expected == "" {
			return false
		}
		if labels[key] != expected {
			return false
		}
	}
	return true
}

func extractServicePorts(spec map[string]interface{}) []int {
	ports := make([]int, 0, 4)
	for _, raw := range interfaceSlice(spec["ports"]) {
		entry := shared.MapValue(raw)
		port := intValue(entry, "port")
		if port > 0 {
			ports = append(ports, port)
		}
	}
	return uniquePositiveInts(ports)
}

func backendService(backend map[string]interface{}) string {
	service := shared.MapValue(backend["service"])
	return strings.TrimSpace(shared.StringValue(service, "name"))
}

func extractContainerProbes(container map[string]interface{}) []models.DiscoveredProbe {
	if len(container) == 0 {
		return nil
	}
	containerName := strings.TrimSpace(shared.StringValue(container, "name"))
	probes := make([]models.DiscoveredProbe, 0, 3)
	for _, probeType := range []string{"readiness", "liveness", "startup"} {
		probe := shared.MapValue(container[probeType+"Probe"])
		if len(probe) == 0 {
			continue
		}

		if httpGet := shared.MapValue(probe["httpGet"]); len(httpGet) > 0 {
			probes = append(probes, models.DiscoveredProbe{
				Type:          probeType,
				Handler:       "httpGet",
				ContainerName: containerName,
				Path:          strings.TrimSpace(shared.StringValue(httpGet, "path")),
				Port:          interfaceStringValue(httpGet["port"]),
			})
			continue
		}
		if tcpSocket := shared.MapValue(probe["tcpSocket"]); len(tcpSocket) > 0 {
			probes = append(probes, models.DiscoveredProbe{
				Type:          probeType,
				Handler:       "tcpSocket",
				ContainerName: containerName,
				Port:          interfaceStringValue(tcpSocket["port"]),
			})
			continue
		}
		if grpc := shared.MapValue(probe["grpc"]); len(grpc) > 0 {
			probes = append(probes, models.DiscoveredProbe{
				Type:          probeType,
				Handler:       "grpc",
				ContainerName: containerName,
				Port:          interfaceStringValue(grpc["port"]),
				Service:       strings.TrimSpace(shared.StringValue(grpc, "service")),
			})
			continue
		}
		if execProbe := shared.MapValue(probe["exec"]); len(execProbe) > 0 {
			probes = append(probes, models.DiscoveredProbe{
				Type:          probeType,
				Handler:       "exec",
				ContainerName: containerName,
				Command:       stringSliceValue(execProbe["command"]),
			})
		}
	}
	if len(probes) == 0 {
		return nil
	}
	return probes
}

func preferredProbeHealthCheckPath(probes []models.DiscoveredProbe) string {
	for _, preferredType := range []string{"readiness", "liveness", "startup"} {
		for _, probe := range probes {
			if probe.Type == preferredType && probe.Handler == "httpGet" && strings.TrimSpace(probe.Path) != "" {
				return strings.TrimSpace(probe.Path)
			}
		}
	}
	return ""
}

func extractContainerEnvVars(container map[string]interface{}) []models.DiscoveredEnvironmentVariable {
	if len(container) == 0 {
		return nil
	}
	variables := make([]models.DiscoveredEnvironmentVariable, 0, 8)
	for _, raw := range interfaceSlice(container["env"]) {
		entry := shared.MapValue(raw)
		key := strings.TrimSpace(shared.StringValue(entry, "name"))
		if key == "" {
			continue
		}
		valueFrom := shared.MapValue(entry["valueFrom"])
		if len(valueFrom) > 0 {
			sourceType, reference := describeValueFromReference(valueFrom)
			variables = append(variables, models.DiscoveredEnvironmentVariable{
				Key:        key,
				SourceType: sourceType,
				Reference:  reference,
				Importable: false,
			})
			continue
		}
		valueRaw, ok := entry["value"]
		if !ok {
			continue
		}
		variables = append(variables, models.DiscoveredEnvironmentVariable{
			Key:        key,
			Value:      interfaceStringValue(valueRaw),
			SourceType: "plain",
			Importable: true,
		})
	}
	if len(variables) == 0 {
		return nil
	}
	return variables
}

func describeValueFromReference(valueFrom map[string]interface{}) (string, string) {
	if secretRef := shared.MapValue(valueFrom["secretKeyRef"]); len(secretRef) > 0 {
		name := strings.TrimSpace(shared.StringValue(secretRef, "name"))
		key := strings.TrimSpace(shared.StringValue(secretRef, "key"))
		return "secretKeyRef", joinValueFromReference("secret", name, key)
	}
	if configMapRef := shared.MapValue(valueFrom["configMapKeyRef"]); len(configMapRef) > 0 {
		name := strings.TrimSpace(shared.StringValue(configMapRef, "name"))
		key := strings.TrimSpace(shared.StringValue(configMapRef, "key"))
		return "configMapKeyRef", joinValueFromReference("configMap", name, key)
	}
	if fieldRef := shared.MapValue(valueFrom["fieldRef"]); len(fieldRef) > 0 {
		fieldPath := strings.TrimSpace(shared.StringValue(fieldRef, "fieldPath"))
		return "fieldRef", fieldPath
	}
	if resourceFieldRef := shared.MapValue(valueFrom["resourceFieldRef"]); len(resourceFieldRef) > 0 {
		resource := strings.TrimSpace(shared.StringValue(resourceFieldRef, "resource"))
		if divisor := strings.TrimSpace(shared.StringValue(resourceFieldRef, "divisor")); divisor != "" {
			return "resourceFieldRef", resource + " divisor=" + divisor
		}
		return "resourceFieldRef", resource
	}
	return "valueFrom", ""
}

func joinValueFromReference(kind, name, key string) string {
	parts := make([]string, 0, 2)
	if name != "" {
		parts = append(parts, name)
	}
	if key != "" {
		parts = append(parts, key)
	}
	if len(parts) == 0 {
		return kind
	}
	return kind + ":" + strings.Join(parts, "#")
}

func extractContainerResources(container map[string]interface{}) (int, int) {
	if len(container) == 0 {
		return 0, 0
	}
	resources := shared.MapValue(container["resources"])
	requests := shared.MapValue(resources["requests"])
	limits := shared.MapValue(resources["limits"])

	cpu := resourceValue(requests, "cpu")
	if cpu == "" {
		cpu = resourceValue(limits, "cpu")
	}
	memory := resourceValue(requests, "memory")
	if memory == "" {
		memory = resourceValue(limits, "memory")
	}

	return parseCPUMilli(cpu), parseMemoryMi(memory)
}

func resourceValue(source map[string]interface{}, key string) string {
	if len(source) == 0 {
		return ""
	}
	raw, ok := source[key]
	if !ok || raw == nil {
		return ""
	}
	return interfaceStringValue(raw)
}

func stringSliceValue(value interface{}) []string {
	items := interfaceSlice(value)
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		trimmed := interfaceStringValue(raw)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseCPUMilli(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if strings.HasSuffix(trimmed, "m") {
		parsed, err := strconv.Atoi(strings.TrimSuffix(trimmed, "m"))
		if err != nil || parsed <= 0 {
			return 0
		}
		return parsed
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	return int(math.Round(parsed * 1000))
}

func parseMemoryMi(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	suffix := memoryQuantitySuffix(trimmed)
	numberPart := strings.TrimSpace(strings.TrimSuffix(trimmed, suffix))
	if numberPart == "" {
		return 0
	}
	parsed, err := strconv.ParseFloat(numberPart, 64)
	if err != nil || parsed <= 0 {
		return 0
	}
	multiplier, ok := memorySuffixMultiplierMi[suffix]
	if !ok {
		return 0
	}
	return int(math.Round(parsed * multiplier))
}

var memorySuffixMultiplierMi = map[string]float64{
	"":   1.0 / (1024.0 * 1024.0),
	"Ki": 1.0 / 1024.0,
	"Mi": 1,
	"Gi": 1024,
	"Ti": 1024 * 1024,
	"K":  1000.0 / (1024.0 * 1024.0),
	"M":  1000000.0 / (1024.0 * 1024.0),
	"G":  1000000000.0 / (1024.0 * 1024.0),
	"T":  1000000000000.0 / (1024.0 * 1024.0),
}

func memoryQuantitySuffix(value string) string {
	for _, suffix := range []string{"Ki", "Mi", "Gi", "Ti", "K", "M", "G", "T"} {
		if strings.HasSuffix(value, suffix) {
			return suffix
		}
	}
	return ""
}

func interfaceStringValue(value interface{}) string {
	return strings.TrimSpace(fmt.Sprint(value))
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
