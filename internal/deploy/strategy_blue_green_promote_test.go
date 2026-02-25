package deploy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"
	"testing"
	"time"
)

type promoteRoundTripFunc func(*http.Request) (*http.Response, error)

func (f promoteRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func promoteRewriteClient(server *httptest.Server) *http.Client {
	target, _ := url.Parse(server.URL)
	base := server.Client().Transport
	return &http.Client{
		Transport: promoteRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			return base.RoundTrip(clone)
		}),
	}
}

func TestCleanupBlueGreenHelpers(t *testing.T) {
	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleteCalls++
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := promoteRewriteClient(server)
	if err := cleanupBlueGreenSlot(context.Background(), client, "token", "apps", "api-blue", nil); err != nil {
		t.Fatalf("unexpected blue-green slot cleanup error: %v", err)
	}
	if err := cleanupLegacyRollingWorkload(context.Background(), client, "token", "apps", "api", nil); err != nil {
		t.Fatalf("unexpected legacy cleanup error: %v", err)
	}
	if deleteCalls != 4 {
		t.Fatalf("expected 4 delete calls (2+2), got %d", deleteCalls)
	}
}

func TestSwitchBlueGreenCanonicalService(t *testing.T) {
	getCalls := 0
	putCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/services/api"):
			getCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": "apps",
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-blue"},
				},
			})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/services/api"):
			putCalls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := promoteRewriteClient(server)
	err := switchBlueGreenCanonicalService(context.Background(), client, "token", "apps", "api", "api-green")
	if err != nil {
		t.Fatalf("unexpected canonical switch error: %v", err)
	}
	if getCalls == 0 || putCalls == 0 {
		t.Fatalf("expected get/put calls for canonical service switch, got get=%d put=%d", getCalls, putCalls)
	}
}

func TestPromoteBlueGreenWithObservationAndLogger(t *testing.T) {
	namespace := "releasea-apps-production"
	logUpdates := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-blue"},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/workers/services/svc-1/blue-green/primary":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api-green":
			_ = json.NewEncoder(w).Encode(models.DeploymentInfo{
				Status: models.DeploymentStatus{
					AvailableReplicas: 1,
					Replicas:          1,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/pods":
			_ = json.NewEncoder(w).Encode(models.PodList{Items: []models.PodInfo{}})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/deploys/dep-bg/logs":
			logUpdates++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	t.Setenv("WORKER_BLUE_GREEN_OBSERVATION_SECONDS", "1")

	logger := platform.NewDeployLogger(server.Client(), models.Config{ApiBaseURL: server.URL}, platform.NewTokenManager("access-token"), "dep-bg")
	start := time.Now()
	slot, err := promoteBlueGreen(
		context.Background(),
		server.Client(),
		models.Config{ApiBaseURL: server.URL},
		platform.NewTokenManager("access-token"),
		models.ServiceConfig{
			Name: "api",
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type:             "blue-green",
				BlueGreenPrimary: "blue",
			},
		},
		"svc-1",
		"prod",
		"api",
		logger,
	)
	if err != nil {
		t.Fatalf("unexpected promoteBlueGreen observation error: %v", err)
	}
	if slot != "green" {
		t.Fatalf("expected promoted slot green, got %q", slot)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Fatalf("expected observation delay to run before completion")
	}
	if logUpdates == 0 {
		t.Fatalf("expected promote blue-green log updates")
	}
}

func TestPromoteBlueGreenReadinessFailureRollback(t *testing.T) {
	namespace := "releasea-apps-production"
	canonicalTarget := "api-blue"
	slotUpdates := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Service",
				"metadata": map[string]interface{}{
					"name":      "api",
					"namespace": namespace,
				},
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": canonicalTarget},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/namespaces/"+namespace+"/services/api":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			spec := body["spec"].(map[string]interface{})
			selector := spec["selector"].(map[string]interface{})
			canonicalTarget = selector["app"].(string)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Path == "/workers/services/svc-1/blue-green/primary":
			slotUpdates++
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/apis/apps/v1/namespaces/"+namespace+"/deployments/api-green":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": map[string]interface{}{
					"conditions": []interface{}{
						map[string]interface{}{
							"type":   "Progressing",
							"status": "False",
							"reason": "ImagePullBackOff",
						},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	setupKubeEnvForTest(t, server.URL)
	t.Setenv("WORKER_BLUE_GREEN_OBSERVATION_SECONDS", "0")

	_, err := promoteBlueGreen(
		context.Background(),
		server.Client(),
		models.Config{ApiBaseURL: server.URL},
		platform.NewTokenManager("access-token"),
		models.ServiceConfig{
			Name: "api",
			DeploymentStrategy: models.DeploymentStrategyConfig{
				Type:             "blue-green",
				BlueGreenPrimary: "blue",
			},
		},
		"svc-1",
		"prod",
		"api",
		nil,
	)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unhealthy") {
		t.Fatalf("expected unhealthy promoted slot error, got %v", err)
	}
	if slotUpdates != 2 {
		t.Fatalf("expected slot update and rollback update calls, got %d", slotUpdates)
	}
	if canonicalTarget != "api-blue" {
		t.Fatalf("expected canonical service rollback to api-blue, got %q", canonicalTarget)
	}
}
