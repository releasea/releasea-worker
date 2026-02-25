package deploy

import (
	"reflect"
	"releaseaworker/internal/platform/models"
	"testing"
)

func TestIsWorkloadReadinessRequired(t *testing.T) {
	if IsWorkloadReadinessRequired("static-site", "") {
		t.Fatalf("static-site should skip workload readiness")
	}
	if IsWorkloadReadinessRequired("service", "tpl-cronjob") {
		t.Fatalf("cronjob template should skip workload readiness")
	}
	if !IsWorkloadReadinessRequired("service", "") {
		t.Fatalf("regular service should require readiness")
	}
}

func TestResolveWorkloadTargetsWithHints(t *testing.T) {
	hints := []deploymentResourceHint{
		{name: "api-v2"},
		{name: "api-v2"},
		{name: "api-v3"},
	}

	if got := resolveWorkloadTargetsWithHints("static-site", "", models.DeploymentStrategyConfig{}, "site", hints); got != nil {
		t.Fatalf("expected nil targets for static-site, got %v", got)
	}

	rollingTargets := resolveWorkloadTargetsWithHints("service", "", models.DeploymentStrategyConfig{Type: "rolling"}, "api", hints)
	if want := []string{"api-v2", "api-v3"}; !reflect.DeepEqual(rollingTargets, want) {
		t.Fatalf("expected %v, got %v", want, rollingTargets)
	}

	canaryTargets := resolveWorkloadTargetsWithHints("service", "", models.DeploymentStrategyConfig{Type: "canary", CanaryPercent: 15}, "api", nil)
	if want := []string{"api", "api-canary"}; !reflect.DeepEqual(canaryTargets, want) {
		t.Fatalf("expected %v, got %v", want, canaryTargets)
	}

	blueGreenTargets := resolveWorkloadTargetsWithHints("service", "", models.DeploymentStrategyConfig{Type: "blue-green", BlueGreenPrimary: "green"}, "api", nil)
	if want := []string{"api-green", "api-blue"}; !reflect.DeepEqual(blueGreenTargets, want) {
		t.Fatalf("expected %v, got %v", want, blueGreenTargets)
	}
}

func TestResolveWorkloadTargetWrappers(t *testing.T) {
	serviceConfig := models.ServiceConfig{
		Type:             "service",
		DeployTemplateID: "",
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type:          "canary",
			CanaryPercent: 10,
		},
	}
	if got := resolveStrategyDeploymentTargets(serviceConfig, "api"); !reflect.DeepEqual(got, []string{"api", "api-canary"}) {
		t.Fatalf("unexpected strategy target wrapper result: %v", got)
	}

	servicePayload := models.ServicePayload{
		Type:             "service",
		DeployTemplateID: "",
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type: "blue-green",
		},
	}
	targets := ResolveServicePayloadDeploymentTargets(servicePayload, "api")
	if len(targets) != 2 {
		t.Fatalf("expected payload deployment targets, got %v", targets)
	}

	payload := map[string]interface{}{
		"resources": []interface{}{
			map[string]interface{}{
				"kind": "Deployment",
				"metadata": map[string]interface{}{
					"name": "api-canary",
				},
			},
		},
	}
	readinessTargets := resolveDeployReadinessTargets(serviceConfig, "api", payload)
	if len(readinessTargets) == 0 {
		t.Fatalf("expected readiness targets from wrapper")
	}
	readinessTargets = ResolveServicePayloadDeployReadinessTargets(servicePayload, "api", payload)
	if len(readinessTargets) == 0 {
		t.Fatalf("expected payload readiness targets from wrapper")
	}
}

func TestResolveDeployNamespaceFromPayload(t *testing.T) {
	payload := map[string]interface{}{
		"resources": []interface{}{
			map[string]interface{}{
				"kind": "Deployment",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": "custom-ns",
				},
			},
		},
	}
	if got := ResolveDeployNamespaceFromPayload(payload, "fallback"); got != "custom-ns" {
		t.Fatalf("expected custom-ns, got %q", got)
	}
	if got := ResolveDeployNamespaceFromPayload(nil, "fallback"); got != "fallback" {
		t.Fatalf("expected fallback for empty payload, got %q", got)
	}
}

func TestExtractDeploymentResourceHints(t *testing.T) {
	payload := map[string]interface{}{
		"resources": []interface{}{
			map[string]interface{}{
				"kind": "Service",
				"metadata": map[string]interface{}{
					"name": "api",
				},
			},
			map[string]interface{}{
				"kind": "Deployment",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": "apps",
				},
			},
		},
	}
	hints := extractDeploymentResourceHints(payload)
	if len(hints) != 1 {
		t.Fatalf("expected 1 deployment hint, got %d", len(hints))
	}
	if hints[0].name != "api" || hints[0].namespace != "apps" {
		t.Fatalf("unexpected hint %+v", hints[0])
	}
}

func TestCollectHintedDeploymentNames(t *testing.T) {
	hints := []deploymentResourceHint{
		{name: "a"},
		{name: "a"},
		{name: "b"},
	}
	got := collectHintedDeploymentNames(hints)
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestDeploymentFailureReason(t *testing.T) {
	deploy := models.DeploymentInfo{
		Status: models.DeploymentStatus{
			Conditions: []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}{
				{
					Type:   "Progressing",
					Status: "False",
					Reason: "ProgressDeadlineExceeded",
				},
			},
		},
	}
	failed, reason := deploymentFailureReason(deploy)
	if !failed || reason == "" {
		t.Fatalf("expected deployment failure with reason, got failed=%v reason=%q", failed, reason)
	}
}

func TestWaitingAndTerminatedFailureReason(t *testing.T) {
	failed, reason := waitingFailureReason(&models.ContainerStateWaiting{Reason: "CrashLoopBackOff"})
	if !failed || reason != "CrashLoopBackOff" {
		t.Fatalf("expected crashloop failure, got failed=%v reason=%q", failed, reason)
	}
	failed, reason = waitingFailureReason(&models.ContainerStateWaiting{Reason: "ContainerCreating"})
	if failed {
		t.Fatalf("did not expect failure for ContainerCreating")
	}

	failed, reason = terminatedFailureReason(&models.ContainerStateTerminated{ExitCode: 2, Reason: "Error"})
	if !failed || reason != "Error" {
		t.Fatalf("expected terminated failure, got failed=%v reason=%q", failed, reason)
	}
	failed, _ = terminatedFailureReason(&models.ContainerStateTerminated{ExitCode: 0})
	if failed {
		t.Fatalf("did not expect failure for exit code 0")
	}
}

func TestEvaluatePodFailure(t *testing.T) {
	failedPod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-0"},
		Status: models.PodStatus{
			Phase: "Failed",
		},
	}
	if failed, _ := evaluatePodFailure(failedPod); !failed {
		t.Fatalf("expected failed pod detection")
	}

	waitingPod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-1"},
		Status: models.PodStatus{
			ContainerStatuses: []models.ContainerStatus{
				{
					Name: "api",
					State: models.ContainerState{
						Waiting: &models.ContainerStateWaiting{Reason: "ImagePullBackOff"},
					},
				},
			},
		},
	}
	if failed, _ := evaluatePodFailure(waitingPod); !failed {
		t.Fatalf("expected waiting failure detection")
	}

	restartPod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-2"},
		Status: models.PodStatus{
			ContainerStatuses: []models.ContainerStatus{
				{
					Name:         "api",
					Ready:        false,
					RestartCount: 4,
				},
			},
		},
	}
	if failed, _ := evaluatePodFailure(restartPod); !failed {
		t.Fatalf("expected restart loop detection")
	}

	healthyPod := models.PodInfo{
		Metadata: models.PodMetadata{Name: "api-3"},
		Status: models.PodStatus{
			ContainerStatuses: []models.ContainerStatus{
				{
					Name:         "api",
					Ready:        true,
					RestartCount: 0,
				},
			},
		},
	}
	if failed, _ := evaluatePodFailure(healthyPod); failed {
		t.Fatalf("did not expect healthy pod failure")
	}
}
