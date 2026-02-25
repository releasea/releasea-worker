package rules

import (
	"reflect"
	"releaseaworker/internal/models"
	"testing"
)

func TestCleanupCandidatesForStrategy(t *testing.T) {
	rolling := cleanupCandidatesForStrategy("api", "rolling")
	if want := []string{"api-canary", "api-blue", "api-green"}; !reflect.DeepEqual(rolling, want) {
		t.Fatalf("expected %v, got %v", want, rolling)
	}

	canary := cleanupCandidatesForStrategy("api", "canary")
	if want := []string{"api-blue", "api-green"}; !reflect.DeepEqual(canary, want) {
		t.Fatalf("expected %v, got %v", want, canary)
	}

	blueGreen := cleanupCandidatesForStrategy("api", "blue-green")
	if want := []string{"api-canary"}; !reflect.DeepEqual(blueGreen, want) {
		t.Fatalf("expected %v, got %v", want, blueGreen)
	}

	if got := cleanupCandidatesForStrategy("api", "unknown"); got != nil {
		t.Fatalf("expected nil for unknown strategy, got %v", got)
	}
}

func TestResolveDeployStrategyTypeForServicePayload(t *testing.T) {
	service := models.ServicePayload{
		Type:             "service",
		DeployTemplateID: "tpl-cronjob",
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type: "canary",
		},
	}
	if got := resolveDeployStrategyTypeForServicePayload(service); got != "rolling" {
		t.Fatalf("expected rolling for cronjob payload, got %q", got)
	}
}

func TestCollectDestinationHosts(t *testing.T) {
	data := map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []interface{}{
				map[string]interface{}{
					"route": []interface{}{
						map[string]interface{}{
							"destination": map[string]interface{}{
								"host": "api.apps.svc.cluster.local",
							},
						},
					},
				},
			},
		},
	}
	hosts := map[string]struct{}{}
	collectDestinationHosts(data, hosts)
	if _, ok := hosts["api.apps.svc.cluster.local"]; !ok {
		t.Fatalf("expected destination host collected")
	}
}

func TestHostMatchesWorkloadAlias(t *testing.T) {
	if !hostMatchesWorkloadAlias("api.apps.svc.cluster.local", "api", "apps") {
		t.Fatalf("expected fqdn alias match")
	}
	if !hostMatchesWorkloadAlias("api.apps", "api", "apps") {
		t.Fatalf("expected short host alias match")
	}
	if hostMatchesWorkloadAlias("other.apps.svc.cluster.local", "api", "apps") {
		t.Fatalf("did not expect non-matching alias")
	}
}

func TestIsWorkloadAliasReferenced(t *testing.T) {
	hosts := map[string]struct{}{
		"api.apps.svc.cluster.local": {},
		"web.apps.svc.cluster.local": {},
	}
	if !isWorkloadAliasReferenced(hosts, "api", "apps") {
		t.Fatalf("expected api alias referenced")
	}
	if isWorkloadAliasReferenced(hosts, "jobs", "apps") {
		t.Fatalf("did not expect jobs alias referenced")
	}
}
