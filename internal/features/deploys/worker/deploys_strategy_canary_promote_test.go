package deploy

import (
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"testing"
)

func TestResolveDesiredReplicasForPromote(t *testing.T) {
	if got := resolveDesiredReplicasForPromote(models.ServiceConfig{}); got != 1 {
		t.Fatalf("expected minimum 1 replica, got %d", got)
	}
	if got := resolveDesiredReplicasForPromote(models.ServiceConfig{Replicas: 4}); got != 4 {
		t.Fatalf("expected replicas override, got %d", got)
	}
	if got := resolveDesiredReplicasForPromote(models.ServiceConfig{Replicas: 10, MaxReplicas: 3}); got != 3 {
		t.Fatalf("expected max cap, got %d", got)
	}
}

func TestStampDeploymentPromotion(t *testing.T) {
	resource := map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{},
			},
		},
	}
	stampDeploymentPromotion(resource)
	spec := shared.MapValue(resource["spec"])
	template := shared.MapValue(spec["template"])
	meta := shared.MapValue(template["metadata"])
	annotations := shared.MapValue(meta["annotations"])
	if shared.StringValue(annotations, "releasea.promoted-at") == "" {
		t.Fatalf("expected promotion annotation")
	}
}
