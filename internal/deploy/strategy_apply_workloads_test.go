package deploy

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"releaseaworker/internal/shared"
	"strings"
	"testing"
)

type applyWorkloadTestLogger struct {
	logs    []string
	flushed int
}

func (l *applyWorkloadTestLogger) Logf(_ context.Context, format string, _ ...interface{}) {
	l.logs = append(l.logs, format)
}

func (l *applyWorkloadTestLogger) Flush(_ context.Context) {
	l.flushed++
}

func baseStrategyResources(serviceName string) []map[string]interface{} {
	return []map[string]interface{}{
		{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      serviceName,
				"namespace": "apps",
			},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{},
					"spec": map[string]interface{}{
						"containers": []interface{}{
							map[string]interface{}{"name": serviceName},
						},
					},
				},
			},
		},
		{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name":      serviceName,
				"namespace": "apps",
			},
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{"app": serviceName},
			},
		},
	}
}

func defaultStrategyDeps() Dependencies {
	return Dependencies{
		ApplyResourceFn: func(_ context.Context, _ *http.Client, _ string, _ map[string]interface{}) error {
			return nil
		},
		DeleteResourceFn: func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) error {
			return nil
		},
		ResourceExistsFn: func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) (bool, error) {
			return false, nil
		},
		FetchResourceFn: func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) (map[string]interface{}, error) {
			return nil, nil
		},
		CloneResourceFn: deepClone,
	}
}

func TestApplyServiceWorkloadResourcesRolling(t *testing.T) {
	ctx := context.Background()
	serviceName := "api"
	resources := baseStrategyResources(serviceName)
	logger := &applyWorkloadTestLogger{}
	applyCalls := 0
	deleteCalls := 0

	deps := defaultStrategyDeps()
	deps.ApplyResourceFn = func(_ context.Context, _ *http.Client, _ string, resource map[string]interface{}) error {
		applyCalls++
		if shared.StringValue(resource, "kind") == "Service" {
			spec := shared.MapValue(resource["spec"])
			selector := shared.MapValue(spec["selector"])
			if shared.StringValue(selector, "app") != "api-stable" {
				t.Fatalf("expected canonical selector preserved as api-stable, got %q", shared.StringValue(selector, "app"))
			}
		}
		return nil
	}
	deps.DeleteResourceFn = func(_ context.Context, _ *http.Client, _ string, apiVersion, kind, namespace, name string) error {
		deleteCalls++
		if apiVersion != "autoscaling/v2" || kind != "HorizontalPodAutoscaler" || namespace != "apps" || name != serviceName {
			t.Fatalf("unexpected delete target %s %s %s/%s", apiVersion, kind, namespace, name)
		}
		return nil
	}
	deps.FetchResourceFn = func(_ context.Context, _ *http.Client, _ string, _, kind, _, name string) (map[string]interface{}, error) {
		if kind == "Service" && name == serviceName {
			return map[string]interface{}{
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-stable"},
				},
			}, nil
		}
		return nil, nil
	}

	err := ApplyServiceWorkloadResources(
		ctx,
		&http.Client{},
		"token",
		"apps",
		serviceName,
		resources,
		ServiceConfig{ID: "svc-1", MinReplicas: 1, DeploymentStrategy: DeploymentStrategyConfig{Type: "rolling"}},
		logger,
		deps,
	)
	if err != nil {
		t.Fatalf("unexpected rolling apply error: %v", err)
	}
	if applyCalls != 2 {
		t.Fatalf("expected 2 apply calls for deployment/service, got %d", applyCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("expected 1 delete call for autoscaler cleanup, got %d", deleteCalls)
	}
}

func TestApplyServiceWorkloadResourcesCanaryFirstDeploy(t *testing.T) {
	ctx := context.Background()
	serviceName := "api"
	resources := baseStrategyResources(serviceName)
	applyCalls := 0
	deleteCalls := 0

	deps := defaultStrategyDeps()
	deps.ApplyResourceFn = func(_ context.Context, _ *http.Client, _ string, _ map[string]interface{}) error {
		applyCalls++
		return nil
	}
	deps.DeleteResourceFn = func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) error {
		deleteCalls++
		return nil
	}
	deps.ResourceExistsFn = func(_ context.Context, _ *http.Client, _ string, _, kind, _, name string) (bool, error) {
		if kind == "Deployment" && name == serviceName {
			return false, nil
		}
		return false, nil
	}

	err := ApplyServiceWorkloadResources(
		ctx,
		&http.Client{},
		"token",
		"apps",
		serviceName,
		resources,
		ServiceConfig{
			ID:          "svc-1",
			Replicas:    3,
			MinReplicas: 1,
			DeploymentStrategy: DeploymentStrategyConfig{
				Type:          "canary",
				CanaryPercent: 20,
			},
		},
		nil,
		deps,
	)
	if err != nil {
		t.Fatalf("unexpected canary apply error: %v", err)
	}
	// stable service + stable deploy + canary deploy + canary service
	if applyCalls != 4 {
		t.Fatalf("expected 4 apply calls on first canary deploy, got %d", applyCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("expected autoscaler delete for canary, got %d", deleteCalls)
	}
}

func TestApplyServiceWorkloadResourcesBlueGreen(t *testing.T) {
	ctx := context.Background()
	serviceName := "api"
	resources := baseStrategyResources(serviceName)
	var appliedNames []string

	deps := defaultStrategyDeps()
	deps.ApplyResourceFn = func(_ context.Context, _ *http.Client, _ string, resource map[string]interface{}) error {
		meta := shared.MapValue(resource["metadata"])
		appliedNames = append(appliedNames, shared.StringValue(meta, "name"))
		return nil
	}
	deps.ResourceExistsFn = func(_ context.Context, _ *http.Client, _ string, _, kind, _, name string) (bool, error) {
		if kind == "Deployment" && name == serviceName+"-blue" {
			return false, nil
		}
		if kind == "Deployment" && name == serviceName {
			return true, nil
		}
		return false, nil
	}

	err := ApplyServiceWorkloadResources(
		ctx,
		&http.Client{},
		"token",
		"apps",
		serviceName,
		resources,
		ServiceConfig{
			ID:          "svc-1",
			Replicas:    2,
			MinReplicas: 1,
			DeploymentStrategy: DeploymentStrategyConfig{
				Type:             "blue-green",
				BlueGreenPrimary: "blue",
			},
		},
		nil,
		deps,
	)
	if err != nil {
		t.Fatalf("unexpected blue-green apply error: %v", err)
	}

	// active deployment/service + candidate deployment/service + canonical service
	if len(appliedNames) != 5 {
		t.Fatalf("expected 5 apply calls in blue-green staging, got %d", len(appliedNames))
	}
}

func TestSplitWorkloadResources(t *testing.T) {
	serviceName := "api"
	deployResource := map[string]interface{}{
		"kind": "Deployment",
		"metadata": map[string]interface{}{
			"name": serviceName,
		},
	}
	serviceResource := map[string]interface{}{
		"kind": "Service",
		"metadata": map[string]interface{}{
			"name": serviceName,
		},
	}
	other := map[string]interface{}{"kind": "ConfigMap"}

	deployment, service, others, err := splitWorkloadResources([]map[string]interface{}{deployResource, serviceResource, other}, serviceName)
	if err != nil {
		t.Fatalf("unexpected split error: %v", err)
	}
	if !reflect.DeepEqual(deployment, deployResource) || !reflect.DeepEqual(service, serviceResource) {
		t.Fatalf("unexpected split result deployment/service")
	}
	if len(others) != 1 {
		t.Fatalf("expected one extra resource, got %d", len(others))
	}

	_, _, _, err = splitWorkloadResources([]map[string]interface{}{other}, serviceName)
	if err == nil {
		t.Fatalf("expected split error when deployment/service missing")
	}
}

func TestResolveCanonicalServiceSelector(t *testing.T) {
	deps := defaultStrategyDeps()
	deps.FetchResourceFn = func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) (map[string]interface{}, error) {
		return map[string]interface{}{
			"spec": map[string]interface{}{
				"selector": map[string]interface{}{"app": "api-stable"},
			},
		}, nil
	}
	got := resolveCanonicalServiceSelector(context.Background(), &http.Client{}, "token", "apps", "api", deps)
	if got != "api-stable" {
		t.Fatalf("expected api-stable selector, got %q", got)
	}
}

func TestDependenciesValidate(t *testing.T) {
	deps := Dependencies{}
	if err := deps.validate(); err == nil {
		t.Fatalf("expected missing dependencies error")
	}

	deps.ApplyResourceFn = func(_ context.Context, _ *http.Client, _ string, _ map[string]interface{}) error { return nil }
	deps.DeleteResourceFn = func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) error { return nil }
	deps.ResourceExistsFn = func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) (bool, error) { return true, nil }
	if err := deps.validate(); err != nil {
		t.Fatalf("expected valid deps, got %v", err)
	}
}

func TestApplyServiceWorkloadResourcesValidateError(t *testing.T) {
	err := ApplyServiceWorkloadResources(
		context.Background(),
		&http.Client{},
		"token",
		"apps",
		"api",
		nil,
		ServiceConfig{},
		nil,
		Dependencies{},
	)
	if err == nil || !strings.Contains(err.Error(), "dependency missing") {
		t.Fatalf("expected dependency missing error, got %v", err)
	}
}

func TestStrategyHelpersInApplyPolicy(t *testing.T) {
	service := ServiceConfig{
		DeploymentStrategy: DeploymentStrategyConfig{BlueGreenPrimary: "blue"},
	}
	if got := resolvePrimaryBlueGreenColor(service); got != "blue" {
		t.Fatalf("expected blue primary, got %q", got)
	}
	if got := oppositeBlueGreenColor("blue"); got != "green" {
		t.Fatalf("expected green opposite, got %q", got)
	}
}

func TestSplitWorkloadResourcesFallback(t *testing.T) {
	serviceName := "api"
	deploymentFallback := map[string]interface{}{
		"kind": "Deployment",
		"metadata": map[string]interface{}{
			"name": "other-name",
		},
	}
	serviceFallback := map[string]interface{}{
		"kind": "Service",
		"metadata": map[string]interface{}{
			"name": "other-name",
		},
	}
	deployment, service, _, err := splitWorkloadResources([]map[string]interface{}{deploymentFallback, serviceFallback}, serviceName)
	if err != nil {
		t.Fatalf("expected fallback split success, got %v", err)
	}
	if shared.StringValue(shared.MapValue(deployment["metadata"]), "name") != "other-name" {
		t.Fatalf("unexpected fallback deployment selection")
	}
	if shared.StringValue(shared.MapValue(service["metadata"]), "name") != "other-name" {
		t.Fatalf("unexpected fallback service selection")
	}
}

func TestApplyServiceWorkloadResourcesPropagatesApplyError(t *testing.T) {
	ctx := context.Background()
	serviceName := "api"
	resources := baseStrategyResources(serviceName)
	deps := defaultStrategyDeps()
	deps.ApplyResourceFn = func(_ context.Context, _ *http.Client, _ string, _ map[string]interface{}) error {
		return fmt.Errorf("apply failed")
	}
	err := ApplyServiceWorkloadResources(
		ctx,
		&http.Client{},
		"token",
		"apps",
		serviceName,
		resources,
		ServiceConfig{DeploymentStrategy: DeploymentStrategyConfig{Type: "rolling"}},
		nil,
		deps,
	)
	if err == nil {
		t.Fatalf("expected apply failure")
	}
}
