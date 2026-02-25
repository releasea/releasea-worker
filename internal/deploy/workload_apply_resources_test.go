package deploy

import (
	"releaseaworker/internal/models"
	"releaseaworker/internal/shared"
	"testing"
)

func deploymentResourceForWorkloadTests() map[string]interface{} {
	return map[string]interface{}{
		"kind": "Deployment",
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "api"},
					},
				},
			},
		},
	}
}

func cronjobResourceForWorkloadTests() map[string]interface{} {
	return map[string]interface{}{
		"kind": "CronJob",
		"spec": map[string]interface{}{
			"jobTemplate": map[string]interface{}{
				"spec": map[string]interface{}{
					"template": map[string]interface{}{
						"metadata": map[string]interface{}{},
						"spec": map[string]interface{}{
							"containers": []interface{}{
								map[string]interface{}{"name": "job"},
							},
						},
					},
				},
			},
		},
	}
}

func TestContainerInjectionHelpers(t *testing.T) {
	resource := deploymentResourceForWorkloadTests()
	service := models.ServiceConfig{
		DockerImage: "repo/api:v1",
		CPU:         250,
		Memory:      512,
	}
	injectContainerImage(resource, service)
	injectContainerResources(resource, service)
	containers := resourceContainers(resource)
	if len(containers) != 1 {
		t.Fatalf("expected one container")
	}
	if shared.StringValue(containers[0], "image") != service.DockerImage {
		t.Fatalf("expected injected image")
	}
	resources := shared.MapValue(containers[0]["resources"])
	if len(resources) == 0 {
		t.Fatalf("expected injected container resources")
	}
}

func TestResourceContainersKinds(t *testing.T) {
	deployment := deploymentResourceForWorkloadTests()
	if len(resourceContainers(deployment)) != 1 {
		t.Fatalf("expected deployment containers")
	}
	cronjob := cronjobResourceForWorkloadTests()
	if len(resourceContainers(cronjob)) != 1 {
		t.Fatalf("expected cronjob containers")
	}
	job := map[string]interface{}{
		"kind": "Job",
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{"name": "job"}},
				},
			},
		},
	}
	if len(resourceContainers(job)) != 1 {
		t.Fatalf("expected job containers")
	}
	statefulset := map[string]interface{}{
		"kind": "StatefulSet",
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{map[string]interface{}{"name": "stateful"}},
				},
			},
		},
	}
	if len(resourceContainers(statefulset)) != 1 {
		t.Fatalf("expected statefulset containers")
	}
	if got := resourceContainers(map[string]interface{}{"kind": "Service"}); got != nil {
		t.Fatalf("expected nil containers for unsupported kind")
	}
}

func TestInjectReplicaCount(t *testing.T) {
	resource := deploymentResourceForWorkloadTests()
	injectReplicaCount(resource, models.ServiceConfig{MinReplicas: 2, MaxReplicas: 5})
	spec := shared.MapValue(resource["spec"])
	if spec["replicas"] != 2 {
		t.Fatalf("expected replicas=2, got %#v", spec["replicas"])
	}

	injectReplicaCount(resource, models.ServiceConfig{Replicas: 10, MaxReplicas: 3})
	spec = shared.MapValue(resource["spec"])
	if spec["replicas"] != 3 {
		t.Fatalf("expected replicas capped to max=3, got %#v", spec["replicas"])
	}
}

func TestStampDeployRevision(t *testing.T) {
	deployment := deploymentResourceForWorkloadTests()
	stampDeployRevision(deployment)
	spec := shared.MapValue(deployment["spec"])
	template := shared.MapValue(spec["template"])
	meta := shared.MapValue(template["metadata"])
	annotations := shared.MapValue(meta["annotations"])
	if shared.StringValue(annotations, "releasea.io/deploy-revision") == "" {
		t.Fatalf("expected deployment revision annotation")
	}

	cronjob := cronjobResourceForWorkloadTests()
	stampDeployRevision(cronjob)
	spec = shared.MapValue(cronjob["spec"])
	jobTemplate := shared.MapValue(spec["jobTemplate"])
	jobSpec := shared.MapValue(jobTemplate["spec"])
	template = shared.MapValue(jobSpec["template"])
	meta = shared.MapValue(template["metadata"])
	annotations = shared.MapValue(meta["annotations"])
	if shared.StringValue(annotations, "releasea.io/deploy-revision") == "" {
		t.Fatalf("expected cronjob revision annotation")
	}
}

func TestApplyResourcesYAMLEmpty(t *testing.T) {
	err := applyResourcesYAML(nil, "   ", nil)
	if err == nil {
		t.Fatalf("expected error for empty yaml")
	}
}

func TestDefaultNumericString(t *testing.T) {
	if got := defaultNumericString("", "7"); got != "7" {
		t.Fatalf("expected fallback numeric string, got %q", got)
	}
	if got := defaultNumericString(" 9 ", "7"); got != "9" {
		t.Fatalf("expected trimmed value 9, got %q", got)
	}
}

func TestScrubCronJobFields(t *testing.T) {
	resource := cronjobResourceForWorkloadTests()
	spec := shared.MapValue(resource["spec"])
	spec["timeZone"] = "UTC"
	resource["spec"] = spec

	replacements := map[string]string{
		"scheduleTimezone": "",
		"scheduleCommand":  "",
	}
	err := scrubCronJobFields(resource, replacements)
	if err != nil {
		t.Fatalf("unexpected scrub error: %v", err)
	}
	spec = shared.MapValue(resource["spec"])
	if _, ok := spec["timeZone"]; ok {
		t.Fatalf("expected timeZone removed when empty replacement")
	}
	jobTemplate := shared.MapValue(spec["jobTemplate"])
	jobSpec := shared.MapValue(jobTemplate["spec"])
	template := shared.MapValue(jobSpec["template"])
	podSpec := shared.MapValue(template["spec"])
	containers, _ := podSpec["containers"].([]interface{})
	first := shared.MapValue(containers[0])
	if _, exists := first["command"]; exists {
		t.Fatalf("expected command removed for empty scheduleCommand")
	}
}

func TestApplyHealthCheckProbes(t *testing.T) {
	resource := deploymentResourceForWorkloadTests()
	applyHealthCheckProbes(resource, "healthz", 8080)
	containers := resourceContainers(resource)
	first := containers[0]
	if _, ok := first["readinessProbe"]; !ok {
		t.Fatalf("expected readinessProbe injected")
	}
	if _, ok := first["livenessProbe"]; !ok {
		t.Fatalf("expected livenessProbe injected")
	}
}

func TestIsClusterScopedKind(t *testing.T) {
	if !isClusterScopedKind("Namespace") {
		t.Fatalf("expected namespace as cluster-scoped")
	}
	if isClusterScopedKind("Deployment") {
		t.Fatalf("did not expect deployment as cluster-scoped")
	}
}
