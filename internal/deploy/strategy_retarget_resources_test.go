package deploy

import (
	"testing"

	"releaseaworker/internal/shared"
)

func TestDeepClone(t *testing.T) {
	original := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "api"},
		"spec": map[string]interface{}{
			"replicas": 1,
		},
	}
	cloned := deepClone(original)
	clonedMeta := shared.MapValue(cloned["metadata"])
	clonedMeta["name"] = "api-copy"
	cloned["metadata"] = clonedMeta

	originalMeta := shared.MapValue(original["metadata"])
	if shared.StringValue(originalMeta, "name") != "api" {
		t.Fatalf("expected original map to remain unchanged")
	}
}

func TestRetargetDeployment(t *testing.T) {
	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "old-name",
			"labels": map[string]interface{}{
				"app": "old-app",
			},
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": "old-app",
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "old-app",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "container-old"},
					},
				},
			},
		},
	}

	RetargetDeployment(resource, "api-green", "api-green", 3)

	meta := shared.MapValue(resource["metadata"])
	if shared.StringValue(meta, "name") != "api-green" {
		t.Fatalf("unexpected metadata.name: %q", shared.StringValue(meta, "name"))
	}
	spec := shared.MapValue(resource["spec"])
	if spec["replicas"] != 3 {
		t.Fatalf("expected replicas=3, got %#v", spec["replicas"])
	}
	template := shared.MapValue(spec["template"])
	templateSpec := shared.MapValue(template["spec"])
	containers, _ := templateSpec["containers"].([]interface{})
	first, _ := containers[0].(map[string]interface{})
	if shared.StringValue(first, "name") != "api-green" {
		t.Fatalf("expected container renamed, got %q", shared.StringValue(first, "name"))
	}
}

func TestRetargetService(t *testing.T) {
	resource := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "old-svc",
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"app": "old-app",
			},
		},
	}
	RetargetService(resource, "api-blue", "api-blue")

	meta := shared.MapValue(resource["metadata"])
	if shared.StringValue(meta, "name") != "api-blue" {
		t.Fatalf("expected renamed service")
	}
	spec := shared.MapValue(resource["spec"])
	selector := shared.MapValue(spec["selector"])
	if shared.StringValue(selector, "app") != "api-blue" {
		t.Fatalf("expected selector updated")
	}
}

func TestSanitizeResourceForApply(t *testing.T) {
	resource := map[string]interface{}{
		"kind":   "Service",
		"status": map[string]interface{}{"ok": true},
		"metadata": map[string]interface{}{
			"uid":               "123",
			"resourceVersion":   "7",
			"creationTimestamp": "now",
			"generation":        4,
			"managedFields":     []interface{}{},
			"annotations": map[string]interface{}{
				"kubectl.kubernetes.io/last-applied-configuration": "json",
				"deployment.kubernetes.io/revision":                "2",
				"keep":                                             "true",
			},
		},
		"spec": map[string]interface{}{
			"clusterIP":      "10.0.0.1",
			"clusterIPs":     []interface{}{"10.0.0.1"},
			"ipFamilies":     []interface{}{"IPv4"},
			"ipFamilyPolicy": "SingleStack",
		},
	}

	sanitizeResourceForApply(resource)

	if _, ok := resource["status"]; ok {
		t.Fatalf("status must be removed")
	}
	meta := shared.MapValue(resource["metadata"])
	if _, ok := meta["uid"]; ok {
		t.Fatalf("uid must be removed")
	}
	annotations := shared.MapValue(meta["annotations"])
	if _, ok := annotations["kubectl.kubernetes.io/last-applied-configuration"]; ok {
		t.Fatalf("kubectl annotation must be removed")
	}
	if _, ok := annotations["keep"]; !ok {
		t.Fatalf("custom annotation must remain")
	}
	spec := shared.MapValue(resource["spec"])
	if _, ok := spec["clusterIP"]; ok {
		t.Fatalf("clusterIP must be removed")
	}
}
