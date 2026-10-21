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

func TestPromoteCanaryVirtualServiceKeepsRuleAndMovesWeight(t *testing.T) {
	vs := map[string]interface{}{
		"metadata": map[string]interface{}{"name": "go-service-rule-1"},
		"spec": map[string]interface{}{
			"gateways": []interface{}{"istio-system/releasea-internal-gateway"},
			"hosts":    []interface{}{"go-service.releasea.internal"},
			"http": []interface{}{
				map[string]interface{}{
					"name": "edge-rule-1",
					"route": []interface{}{
						map[string]interface{}{
							"destination": map[string]interface{}{
								"host": "go-service.releasea-apps-development.svc.cluster.local",
								"port": map[string]interface{}{"number": 80},
							},
							"weight": 90,
						},
						map[string]interface{}{
							"destination": map[string]interface{}{
								"host": "go-service-canary.releasea-apps-development.svc.cluster.local",
								"port": map[string]interface{}{"number": 80},
							},
							"weight": 10,
						},
					},
				},
			},
		},
	}

	if !promoteCanaryVirtualService(vs, "go-service", "releasea-apps-development") {
		t.Fatal("expected canary destination to be shifted")
	}
	spec := shared.MapValue(vs["spec"])
	if shared.StringValue(shared.MapValue(vs["metadata"]), "name") != "go-service-rule-1" {
		t.Fatal("rule virtual service identity changed")
	}
	gateways, _ := spec["gateways"].([]interface{})
	if len(gateways) != 1 || gateways[0] != "istio-system/releasea-internal-gateway" {
		t.Fatalf("gateways changed: %#v", spec["gateways"])
	}
	httpRoutes, _ := spec["http"].([]interface{})
	route := shared.MapValue(httpRoutes[0])
	if shared.StringValue(route, "name") != "edge-rule-1" {
		t.Fatalf("route name changed: %#v", route["name"])
	}
	destinations, _ := route["route"].([]interface{})
	if len(destinations) != 1 {
		t.Fatalf("expected one stable destination, got %#v", destinations)
	}
	destination := shared.MapValue(destinations[0])
	if shared.StringValue(shared.MapValue(destination["destination"]), "host") != "go-service.releasea-apps-development.svc.cluster.local" {
		t.Fatalf("stable host not kept: %#v", destination)
	}
	if routeWeight(destination["weight"]) != 100 {
		t.Fatalf("expected weight 100, got %#v", destination["weight"])
	}
}

func TestPromoteCanaryVirtualServiceIgnoresOtherServices(t *testing.T) {
	vs := map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []interface{}{
				map[string]interface{}{
					"route": []interface{}{
						map[string]interface{}{
							"destination": map[string]interface{}{"host": "other-canary.apps.svc.cluster.local"},
							"weight":      100,
						},
					},
				},
			},
		},
	}
	if promoteCanaryVirtualService(vs, "go-service", "apps") {
		t.Fatal("expected unrelated virtual service to stay unchanged")
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
