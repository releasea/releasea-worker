package ops

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

func injectContainerResources(resource map[string]interface{}, service serviceConfig) {
	if resource == nil || service.CPU <= 0 || service.Memory <= 0 {
		return
	}
	containers := resourceContainers(resource)
	if len(containers) == 0 {
		return
	}
	resourceSpec := map[string]interface{}{
		"requests": map[string]interface{}{
			"cpu":    fmt.Sprintf("%dm", service.CPU),
			"memory": fmt.Sprintf("%dMi", service.Memory),
		},
		"limits": map[string]interface{}{
			"cpu":    fmt.Sprintf("%dm", service.CPU),
			"memory": fmt.Sprintf("%dMi", service.Memory),
		},
	}
	for _, item := range containers {
		item["resources"] = resourceSpec
	}
}

func injectContainerImage(resource map[string]interface{}, service serviceConfig) {
	image := strings.TrimSpace(service.DockerImage)
	if resource == nil || image == "" {
		return
	}
	containers := resourceContainers(resource)
	for _, item := range containers {
		item["image"] = image
	}
}

func resourceContainers(resource map[string]interface{}) []map[string]interface{} {
	if resource == nil {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(stringValue(resource, "kind")))
	var containers []interface{}

	switch kind {
	case "deployment":
		spec := mapValue(resource["spec"])
		template := mapValue(spec["template"])
		podSpec := mapValue(template["spec"])
		if c, ok := podSpec["containers"].([]interface{}); ok {
			containers = c
		}
	case "cronjob":
		spec := mapValue(resource["spec"])
		jobTemplate := mapValue(spec["jobTemplate"])
		jobSpec := mapValue(jobTemplate["spec"])
		template := mapValue(jobSpec["template"])
		podSpec := mapValue(template["spec"])
		if c, ok := podSpec["containers"].([]interface{}); ok {
			containers = c
		}
	case "job":
		spec := mapValue(resource["spec"])
		template := mapValue(spec["template"])
		podSpec := mapValue(template["spec"])
		if c, ok := podSpec["containers"].([]interface{}); ok {
			containers = c
		}
	case "statefulset":
		spec := mapValue(resource["spec"])
		template := mapValue(spec["template"])
		podSpec := mapValue(template["spec"])
		if c, ok := podSpec["containers"].([]interface{}); ok {
			containers = c
		}
	default:
		return nil
	}

	resolved := make([]map[string]interface{}, 0, len(containers))
	for _, item := range containers {
		container, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		resolved = append(resolved, container)
	}
	return resolved
}

func injectReplicaCount(resource map[string]interface{}, service serviceConfig) {
	if resource == nil {
		return
	}
	spec := mapValue(resource["spec"])
	if spec == nil {
		return
	}
	replicas := service.MinReplicas
	if replicas < 1 {
		replicas = 1
	}
	if service.Replicas > 0 {
		replicas = service.Replicas
	}
	if service.MaxReplicas > 0 && replicas > service.MaxReplicas {
		replicas = service.MaxReplicas
	}
	spec["replicas"] = replicas
	resource["spec"] = spec
}

func stampDeployRevision(resource map[string]interface{}) {
	if resource == nil {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(stringValue(resource, "kind")))
	stamp := time.Now().UTC().Format(time.RFC3339Nano)

	switch kind {
	case "deployment":
		spec := mapValue(resource["spec"])
		template := mapValue(spec["template"])
		templateMeta := mapValue(template["metadata"])
		annotations := mapValue(templateMeta["annotations"])
		annotations["releasea.io/deploy-revision"] = stamp
		templateMeta["annotations"] = annotations
		template["metadata"] = templateMeta
		spec["template"] = template
		resource["spec"] = spec
	case "cronjob":
		spec := mapValue(resource["spec"])
		jobTemplate := mapValue(spec["jobTemplate"])
		jobSpec := mapValue(jobTemplate["spec"])
		template := mapValue(jobSpec["template"])
		templateMeta := mapValue(template["metadata"])
		annotations := mapValue(templateMeta["annotations"])
		annotations["releasea.io/deploy-revision"] = stamp
		templateMeta["annotations"] = annotations
		template["metadata"] = templateMeta
		jobSpec["template"] = template
		jobTemplate["spec"] = jobSpec
		spec["jobTemplate"] = jobTemplate
		resource["spec"] = spec
	}
}

func stampDeployRevisionKubectl(ctx context.Context, cfg Config, environment, serviceName string, service serviceConfig, logger *deployLogger) {
	if serviceName == "" {
		return
	}
	namespace := resolveNamespace(cfg, environment)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	patch := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"releasea.io/deploy-revision":"%s"}}}}}`, stamp)

	serviceType := strings.ToLower(service.Type)
	kind := "deployment"
	if serviceType == "cronjob" || serviceType == "cron" || strings.TrimSpace(service.ScheduleCron) != "" {
		kind = "cronjob"
	}

	args := []string{"patch", kind, serviceName, "-n", namespace, "-p", patch}
	output, err := runCommandWithInput(ctx, "kubectl", args, "")
	if err != nil {
		if logger != nil {
			logger.Logf(ctx, "deploy revision stamp via kubectl patch: %v", err)
		} else {
			log.Printf("[worker] deploy revision stamp via kubectl patch: %v", err)
		}
		return
	}
	if output != "" && logger != nil {
		logger.Logf(ctx, "deploy revision stamp: %s", output)
	}
}

func applyResourcesYAML(ctx context.Context, resourcesYAML string, logger *deployLogger) error {
	clean := strings.TrimSpace(resourcesYAML)
	if clean == "" {
		return errors.New("deploy resources yaml missing")
	}
	output, err := runCommandWithInput(ctx, "kubectl", []string{"apply", "-f", "-"}, clean)
	if output != "" {
		if logger != nil {
			logger.Logf(ctx, "kubectl apply output: %s", output)
		} else {
			log.Printf("[worker] kubectl apply output: %s", output)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func applyRenderedResources(ctx context.Context, cfg Config, resources []map[string]interface{}, environment string, ctxData deployContext, logger *deployLogger) error {
	if len(resources) == 0 {
		return errors.New("deploy resources missing")
	}
	defaultNamespace := resolveNamespace(cfg, environment)
	if err := validateAppNamespace(defaultNamespace); err != nil {
		return fmt.Errorf("deploy blocked: %w", err)
	}
	namespaces := map[string]struct{}{}
	serviceName := toKubeName(ctxData.Service.Name)
	if serviceName == "" {
		serviceName = toKubeName(ctxData.Service.ID)
	}
	canaryOnly := resolveDeployStrategyType(ctxData.Service) == "canary"
	blueGreenManaged := resolveDeployStrategyType(ctxData.Service) == "blue-green"

	for _, resource := range resources {
		if resource == nil {
			continue
		}
		kind, _ := resource["kind"].(string)
		if isClusterScopedKind(kind) {
			continue
		}
		meta := mapValue(resource["metadata"])
		namespace := ""
		if meta != nil {
			if value, ok := meta["namespace"].(string); ok {
				namespace = strings.TrimSpace(value)
			}
		}
		if namespace == "" {
			namespace = defaultNamespace
			if meta != nil {
				meta["namespace"] = namespace
				resource["metadata"] = meta
			}
		} else {
			normalized := normalizeNamespace(namespace)
			if normalized != "" && normalized != namespace {
				namespace = normalized
				if meta != nil {
					meta["namespace"] = namespace
					resource["metadata"] = meta
				}
				if logger != nil {
					logger.Logf(ctx, "normalized namespace to %s", namespace)
				}
			}
		}
		if namespace != "" {
			namespaces[namespace] = struct{}{}
		}
	}

	client, token, err := kubeClient()
	if err != nil {
		return err
	}
	for namespace := range namespaces {
		if err := ensureNamespace(ctx, client, token, namespace); err != nil {
			return err
		}
	}
	firstCanaryDeploy := false
	if canaryOnly && serviceName != "" {
		stableNamespace := defaultNamespace
		for _, resource := range resources {
			if resource == nil {
				continue
			}
			kind := strings.ToLower(strings.TrimSpace(stringValue(resource, "kind")))
			if kind != "deployment" {
				continue
			}
			meta := mapValue(resource["metadata"])
			name := strings.TrimSpace(stringValue(meta, "name"))
			if name != serviceName {
				continue
			}
			if ns := strings.TrimSpace(stringValue(meta, "namespace")); ns != "" {
				stableNamespace = ns
			}
			break
		}
		stableExists, existsErr := resourceExists(ctx, client, token, "apps/v1", "Deployment", stableNamespace, serviceName)
		if existsErr != nil {
			return existsErr
		}
		firstCanaryDeploy = !stableExists
	}

	for _, resource := range resources {
		if resource == nil {
			continue
		}
		resource = normalizeResourceNumbers(resource)
		injectContainerImage(resource, ctxData.Service)
		injectContainerResources(resource, ctxData.Service)
		kind, _ := resource["kind"].(string)
		if isClusterScopedKind(kind) {
			if logger != nil {
				logger.Logf(ctx, "skipping cluster scoped %s from deploy resources", kind)
			}
			continue
		}
		if strings.EqualFold(kind, "VirtualService") {
			if logger != nil {
				meta, _ := resource["metadata"].(map[string]interface{})
				name, _ := meta["name"].(string)
				if name == "" {
					name = "virtualservice"
				}
				logger.Logf(ctx, "skipping %s from deploy resources", name)
			}
			continue
		}
		meta, _ := resource["metadata"].(map[string]interface{})
		name, _ := meta["name"].(string)
		name = strings.TrimSpace(name)
		if canaryOnly && serviceName != "" && name == serviceName && (strings.EqualFold(kind, "Deployment") || strings.EqualFold(kind, "Service")) {
			if firstCanaryDeploy {
				// First canary deploy: bootstrap stable once with the new version.
				stableResource := renderTemplateResource(resource, map[string]string{})
				if stableResource != nil {
					if strings.EqualFold(kind, "Deployment") {
						injectReplicaCount(stableResource, ctxData.Service)
					}
					stampDeployRevision(stableResource)
					if logger != nil {
						logger.Logf(ctx, "applying %s %s (canary bootstrap)", kind, serviceName)
					}
					if err := applyResource(ctx, client, token, stableResource); err != nil {
						return err
					}
					if logger != nil {
						logger.Flush(ctx)
					}
				}
			}

			// Canary strategy: keep stable untouched after bootstrap and update canary only.
			canaryResource := renderTemplateResource(resource, map[string]string{})
			if canaryResource != nil {
				canaryMeta := mapValue(canaryResource["metadata"])
				if canaryMeta != nil {
					canaryMeta["name"] = serviceName + "-canary"
					canaryResource["metadata"] = canaryMeta
				}
				if strings.EqualFold(kind, "Deployment") {
					injectReplicaCount(canaryResource, ctxData.Service)
				}
				stampDeployRevision(canaryResource)
				if logger != nil {
					logger.Logf(ctx, "applying %s %s-canary (canary only, stable unchanged)", kind, serviceName)
				}
				if err := applyResource(ctx, client, token, canaryResource); err != nil {
					return err
				}
				if logger != nil {
					logger.Flush(ctx)
				}
			}
			continue
		}
		if blueGreenManaged && serviceName != "" && name == serviceName && (strings.EqualFold(kind, "Deployment") || strings.EqualFold(kind, "Service")) {
			// Blue/green strategy manages stable routing and slots explicitly.
			if logger != nil {
				logger.Logf(ctx, "skipping %s %s (blue-green managed resource)", kind, serviceName)
			}
			continue
		}
		if strings.EqualFold(kind, "Deployment") && !canaryOnly && !blueGreenManaged {
			injectReplicaCount(resource, ctxData.Service)
		}
		stampDeployRevision(resource)
		if logger != nil {
			if kind != "" && name != "" {
				logger.Logf(ctx, "applying %s %s", kind, name)
			}
		}
		if err := applyResource(ctx, client, token, resource); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}
	return nil
}

func applyDeployResources(ctx context.Context, cfg Config, ctxData deployContext, environment string, logger *deployLogger) error {
	if ctxData.Template == nil || len(ctxData.Template.Resources) == 0 {
		return errors.New("deploy template missing")
	}
	if ctxData.Service.DockerImage == "" {
		return errors.New("docker image missing")
	}
	serviceName := toKubeName(ctxData.Service.Name)
	if serviceName == "" {
		serviceName = toKubeName(ctxData.Service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}
	namespace := resolveNamespace(cfg, environment)
	if err := validateAppNamespace(namespace); err != nil {
		return fmt.Errorf("deploy blocked: %w", err)
	}
	internalHost := fmt.Sprintf("%s.%s", serviceName, cfg.InternalDomain)
	externalHost := fmt.Sprintf("%s.%s", serviceName, cfg.ExternalDomain)

	plainEnv, secretEnv, err := resolveEnvVars(ctx, cfg, ctxData, environment)
	if err != nil {
		return err
	}

	replacements := map[string]string{
		"serviceName":      serviceName,
		"namespace":        namespace,
		"image":            ctxData.Service.DockerImage,
		"port":             strconv.Itoa(resolvePort(ctxData.Service.Port)),
		"healthCheckPath":  strings.TrimSpace(ctxData.Service.HealthCheckPath),
		"internalHost":     internalHost,
		"externalHost":     externalHost,
		"internalGateway":  cfg.InternalGateway,
		"externalGateway":  cfg.ExternalGateway,
		"scheduleCron":     strings.TrimSpace(ctxData.Service.ScheduleCron),
		"scheduleTimezone": strings.TrimSpace(ctxData.Service.ScheduleTimezone),
		"scheduleCommand":  strings.TrimSpace(ctxData.Service.ScheduleCommand),
		"scheduleRetries":  defaultNumericString(ctxData.Service.ScheduleRetries, "0"),
		"scheduleTimeout":  defaultNumericString(ctxData.Service.ScheduleTimeout, "0"),
	}

	client, token, err := kubeClient()
	if err != nil {
		return err
	}
	if err := ensureNamespace(ctx, client, token, namespace); err != nil {
		return err
	}

	if len(secretEnv) > 0 {
		secretResource := buildSecretResource(serviceName, namespace, secretEnv)
		if logger != nil {
			logger.Logf(ctx, "applying Secret %s", serviceName)
		}
		if err := applyResource(ctx, client, token, secretResource); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	renderedResources := make([]map[string]interface{}, 0, len(ctxData.Template.Resources))
	for _, resource := range ctxData.Template.Resources {
		rendered := renderTemplateResource(resource, replacements)
		rendered = normalizeResourceNumbers(rendered)
		if err := scrubCronJobFields(rendered, replacements); err != nil {
			return err
		}
		applyHealthCheckProbes(rendered, replacements["healthCheckPath"], resolvePort(ctxData.Service.Port))
		if kind, _ := rendered["kind"].(string); strings.EqualFold(kind, "VirtualService") {
			if logger != nil {
				meta, _ := rendered["metadata"].(map[string]interface{})
				name, _ := meta["name"].(string)
				if name == "" {
					name = "virtualservice"
				}
				logger.Logf(ctx, "skipping %s from deploy template", name)
			}
			continue
		}
		if err := injectEnvVars(rendered, plainEnv, secretEnv, serviceName); err != nil {
			return err
		}
		injectContainerResources(rendered, ctxData.Service)
		stampDeployRevision(rendered)
		renderedResources = append(renderedResources, rendered)
	}
	if err := applyServiceWorkloadResources(ctx, client, token, namespace, serviceName, renderedResources, ctxData.Service, logger); err != nil {
		return err
	}
	return nil
}

func defaultNumericString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func scrubCronJobFields(resource map[string]interface{}, replacements map[string]string) error {
	kind := strings.ToLower(strings.TrimSpace(stringValue(resource, "kind")))
	if kind != "cronjob" {
		return nil
	}

	spec := mapValue(resource["spec"])
	if spec == nil {
		return nil
	}
	if strings.TrimSpace(replacements["scheduleTimezone"]) == "" {
		delete(spec, "timeZone")
	}

	command := strings.TrimSpace(replacements["scheduleCommand"])
	if command == "" {
		jobTemplate := mapValue(spec["jobTemplate"])
		jobSpec := mapValue(jobTemplate["spec"])
		template := mapValue(jobSpec["template"])
		podSpec := mapValue(template["spec"])
		containers, ok := podSpec["containers"].([]interface{})
		if !ok {
			return nil
		}
		for _, item := range containers {
			container, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			delete(container, "command")
		}
	}
	return nil
}

func applyHealthCheckProbes(resource map[string]interface{}, rawPath string, port int) {
	if port <= 0 {
		return
	}
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	kind := strings.ToLower(strings.TrimSpace(stringValue(resource, "kind")))
	if kind != "deployment" {
		return
	}
	spec := mapValue(resource["spec"])
	if spec == nil {
		return
	}
	template := mapValue(spec["template"])
	if template == nil {
		return
	}
	podSpec := mapValue(template["spec"])
	if podSpec == nil {
		return
	}
	containers, ok := podSpec["containers"].([]interface{})
	if !ok {
		return
	}
	for _, item := range containers {
		container, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if _, exists := container["readinessProbe"]; !exists {
			container["readinessProbe"] = map[string]interface{}{
				"httpGet": map[string]interface{}{
					"path": path,
					"port": port,
				},
				"initialDelaySeconds": 5,
				"periodSeconds":       10,
				"timeoutSeconds":      2,
				"failureThreshold":    3,
			}
		}
		if _, exists := container["livenessProbe"]; !exists {
			container["livenessProbe"] = map[string]interface{}{
				"httpGet": map[string]interface{}{
					"path": path,
					"port": port,
				},
				"initialDelaySeconds": 15,
				"periodSeconds":       20,
				"timeoutSeconds":      2,
				"failureThreshold":    3,
			}
		}
	}
}

func isClusterScopedKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "namespace",
		"clusterrole",
		"clusterrolebinding",
		"customresourcedefinition",
		"mutatingwebhookconfiguration",
		"validatingwebhookconfiguration",
		"persistentvolume",
		"storageclass",
		"node",
		"priorityclass",
		"volumeattachment",
		"podsecuritypolicy",
		"certificatesigningrequest",
		"runtimeclass",
		"clusterissuer":
		return true
	default:
		return false
	}
}
