package deploy

import (
	"context"
	"net/http"
	"reflect"
	"releaseaworker/internal/shared"
	"testing"
)

type testLogger struct {
	logs    []string
	flushed int
}

func (l *testLogger) Logf(_ context.Context, format string, args ...interface{}) {
	l.logs = append(l.logs, format)
}

func (l *testLogger) Flush(_ context.Context) {
	l.flushed++
}

func TestResolveDesiredReplicas(t *testing.T) {
	tests := []struct {
		name    string
		service ServiceConfig
		want    int
	}{
		{
			name:    "default minimum to one",
			service: ServiceConfig{},
			want:    1,
		},
		{
			name: "explicit replicas override min",
			service: ServiceConfig{
				MinReplicas: 1,
				Replicas:    4,
				MaxReplicas: 10,
			},
			want: 4,
		},
		{
			name: "max limits desired replicas",
			service: ServiceConfig{
				MinReplicas: 5,
				MaxReplicas: 3,
			},
			want: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDesiredReplicas(tc.service); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestResolveCanaryReplicas(t *testing.T) {
	service := ServiceConfig{
		Replicas: 4,
		DeploymentStrategy: DeploymentStrategyConfig{
			CanaryPercent: 50,
		},
	}
	if got := resolveCanaryReplicas(service); got != 2 {
		t.Fatalf("expected 2 canary replicas, got %d", got)
	}

	service = ServiceConfig{
		Replicas: 5,
		DeploymentStrategy: DeploymentStrategyConfig{
			CanaryPercent: 1,
		},
	}
	if got := resolveCanaryReplicas(service); got != 1 {
		t.Fatalf("expected minimum 1 canary replica, got %d", got)
	}
}

func TestApplyDeploymentPolicy(t *testing.T) {
	resource := map[string]interface{}{
		"kind": "Deployment",
		"spec": map[string]interface{}{},
	}
	applyDeploymentPolicy(resource, ServiceConfig{Replicas: 3})
	spec := shared.MapValue(resource["spec"])
	if spec["replicas"] != 3 {
		t.Fatalf("expected replicas=3, got %#v", spec["replicas"])
	}
	strategy := shared.MapValue(spec["strategy"])
	if shared.StringValue(strategy, "type") != "RollingUpdate" {
		t.Fatalf("expected rolling strategy, got %#v", strategy["type"])
	}

	serviceResource := map[string]interface{}{
		"kind": "Service",
		"spec": map[string]interface{}{"unchanged": true},
	}
	before := map[string]interface{}{"unchanged": true}
	applyDeploymentPolicy(serviceResource, ServiceConfig{Replicas: 9})
	after := shared.MapValue(serviceResource["spec"])
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("expected non-deployment resource unchanged, got %#v", after)
	}
}

func TestSyncAutoscalerResourceDeletesForCronjobAndStrategies(t *testing.T) {
	ctx := context.Background()
	client := &http.Client{}
	deleteCalls := 0
	deps := Dependencies{
		ApplyResourceFn: func(_ context.Context, _ *http.Client, _ string, _ map[string]interface{}) error {
			t.Fatalf("apply should not be called")
			return nil
		},
		DeleteResourceFn: func(_ context.Context, _ *http.Client, _ string, apiVersion, kind, namespace, name string) error {
			deleteCalls++
			if apiVersion != "autoscaling/v2" || kind != "HorizontalPodAutoscaler" {
				t.Fatalf("unexpected delete target %s %s", apiVersion, kind)
			}
			if namespace != "ns" || name != "svc" {
				t.Fatalf("unexpected delete name %s/%s", namespace, name)
			}
			return nil
		},
		ResourceExistsFn: func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) (bool, error) {
			return false, nil
		},
	}

	if err := syncAutoscalerResource(ctx, client, "token", "ns", "svc", ServiceConfig{
		DeployTemplateID: "tpl-cronjob",
	}, nil, deps); err != nil {
		t.Fatalf("unexpected error for cronjob: %v", err)
	}
	if err := syncAutoscalerResource(ctx, client, "token", "ns", "svc", ServiceConfig{
		DeploymentStrategy: DeploymentStrategyConfig{Type: "canary"},
	}, nil, deps); err != nil {
		t.Fatalf("unexpected error for canary: %v", err)
	}
	if err := syncAutoscalerResource(ctx, client, "token", "ns", "svc", ServiceConfig{
		MinReplicas: 3,
		MaxReplicas: 3,
	}, nil, deps); err != nil {
		t.Fatalf("unexpected error for disabled autoscaling: %v", err)
	}

	if deleteCalls != 3 {
		t.Fatalf("expected 3 delete calls, got %d", deleteCalls)
	}
}

func TestSyncAutoscalerResourceAppliesHPA(t *testing.T) {
	ctx := context.Background()
	client := &http.Client{}
	logger := &testLogger{}
	applyCalls := 0
	var applied map[string]interface{}

	deps := Dependencies{
		ApplyResourceFn: func(_ context.Context, _ *http.Client, _ string, resource map[string]interface{}) error {
			applyCalls++
			applied = resource
			return nil
		},
		DeleteResourceFn: func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) error {
			t.Fatalf("delete should not be called for valid autoscaling")
			return nil
		},
		ResourceExistsFn: func(_ context.Context, _ *http.Client, _ string, _, _, _, _ string) (bool, error) {
			return false, nil
		},
	}

	err := syncAutoscalerResource(ctx, client, "token", "apps", "api", ServiceConfig{
		ID:          "svc-1",
		MinReplicas: 2,
		MaxReplicas: 8,
		CPU:         75,
	}, logger, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if applyCalls != 1 {
		t.Fatalf("expected one apply call, got %d", applyCalls)
	}
	if logger.flushed == 0 {
		t.Fatalf("expected logger flush")
	}

	spec := shared.MapValue(applied["spec"])
	if spec["minReplicas"] != 2 || spec["maxReplicas"] != 8 {
		t.Fatalf("unexpected HPA replica bounds: %#v", spec)
	}
}
