package deploy

import (
	"encoding/json"
	"strings"

	commonvalues "releaseaworker/internal/shared"
)

func cloneResource(resource map[string]interface{}, deps Dependencies) map[string]interface{} {
	if deps.CloneResourceFn != nil {
		return deps.CloneResourceFn(resource)
	}
	return deepClone(resource)
}

func deepClone(resource map[string]interface{}) map[string]interface{} {
	if resource == nil {
		return map[string]interface{}{}
	}
	raw, err := json.Marshal(resource)
	if err != nil {
		return map[string]interface{}{}
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]interface{}{}
	}
	return out
}

func resourceName(resource map[string]interface{}) string {
	meta := commonvalues.MapValue(resource["metadata"])
	return strings.TrimSpace(commonvalues.StringValue(meta, "name"))
}

func RetargetDeployment(resource map[string]interface{}, resourceName, appLabel string, replicas int) {
	meta := commonvalues.MapValue(resource["metadata"])
	meta["name"] = resourceName
	labels := commonvalues.MapValue(meta["labels"])
	labels["app"] = appLabel
	meta["labels"] = labels
	resource["metadata"] = meta

	spec := commonvalues.MapValue(resource["spec"])
	if replicas <= 0 {
		replicas = 1
	}
	spec["replicas"] = replicas

	selector := commonvalues.MapValue(spec["selector"])
	matchLabels := commonvalues.MapValue(selector["matchLabels"])
	matchLabels["app"] = appLabel
	selector["matchLabels"] = matchLabels
	spec["selector"] = selector

	template := commonvalues.MapValue(spec["template"])
	templateMeta := commonvalues.MapValue(template["metadata"])
	templateLabels := commonvalues.MapValue(templateMeta["labels"])
	templateLabels["app"] = appLabel
	templateMeta["labels"] = templateLabels
	template["metadata"] = templateMeta

	templateSpec := commonvalues.MapValue(template["spec"])
	containers, ok := templateSpec["containers"].([]interface{})
	if ok && len(containers) > 0 {
		if first, ok := containers[0].(map[string]interface{}); ok {
			first["name"] = appLabel
			containers[0] = first
			templateSpec["containers"] = containers
		}
	}
	template["spec"] = templateSpec
	spec["template"] = template
	resource["spec"] = spec
}

func RetargetService(resource map[string]interface{}, resourceName, appSelector string) {
	meta := commonvalues.MapValue(resource["metadata"])
	meta["name"] = resourceName
	labels := commonvalues.MapValue(meta["labels"])
	labels["app"] = appSelector
	meta["labels"] = labels
	resource["metadata"] = meta

	spec := commonvalues.MapValue(resource["spec"])
	selector := commonvalues.MapValue(spec["selector"])
	selector["app"] = appSelector
	spec["selector"] = selector
	resource["spec"] = spec
}

func sanitizeResourceForApply(resource map[string]interface{}) {
	if resource == nil {
		return
	}
	delete(resource, "status")
	meta := commonvalues.MapValue(resource["metadata"])
	if len(meta) == 0 {
		return
	}
	delete(meta, "uid")
	delete(meta, "creationTimestamp")
	delete(meta, "resourceVersion")
	delete(meta, "generation")
	delete(meta, "managedFields")
	annotations := commonvalues.MapValue(meta["annotations"])
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	delete(annotations, "deployment.kubernetes.io/revision")
	if len(annotations) == 0 {
		delete(meta, "annotations")
	} else {
		meta["annotations"] = annotations
	}

	kind := strings.ToLower(strings.TrimSpace(commonvalues.StringValue(resource, "kind")))
	if kind == "service" {
		spec := commonvalues.MapValue(resource["spec"])
		delete(spec, "clusterIP")
		delete(spec, "clusterIPs")
		delete(spec, "ipFamilies")
		delete(spec, "ipFamilyPolicy")
		resource["spec"] = spec
	}
	resource["metadata"] = meta
}
