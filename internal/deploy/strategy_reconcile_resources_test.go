package deploy

import (
	"reflect"
	"testing"
)

func TestFilterStrategyWorkloadResources(t *testing.T) {
	serviceName := "api"
	resources := []map[string]interface{}{
		{
			"kind": "Deployment",
			"metadata": map[string]interface{}{
				"name": "other-deploy",
			},
		},
		{
			"kind": "Deployment",
			"metadata": map[string]interface{}{
				"name": serviceName,
			},
		},
		{
			"kind": "Service",
			"metadata": map[string]interface{}{
				"name": serviceName,
			},
		},
		{
			"kind": "ConfigMap",
		},
	}

	filtered := filterStrategyWorkloadResources(resources, serviceName)
	if len(filtered) != 2 {
		t.Fatalf("expected deployment+service filtered, got %d", len(filtered))
	}
	if got := filtered[0]["kind"]; !reflect.DeepEqual(got, "Deployment") {
		t.Fatalf("expected first filtered resource deployment, got %#v", got)
	}
	if got := filtered[1]["kind"]; !reflect.DeepEqual(got, "Service") {
		t.Fatalf("expected second filtered resource service, got %#v", got)
	}
}

func TestStrategyResourceSummary(t *testing.T) {
	if got := strategyResourceSummary("canary", false); got == "" {
		t.Fatalf("expected non-empty canary summary")
	}
	if got := strategyResourceSummary("blue-green", true); got == "" {
		t.Fatalf("expected non-empty blue-green completion summary")
	}
	if got := strategyResourceSummary("rolling", true); got != "Rolling resources applied" {
		t.Fatalf("unexpected rolling completion summary: %q", got)
	}
}
