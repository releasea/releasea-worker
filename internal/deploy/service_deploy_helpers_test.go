package deploy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"releaseaworker/internal/models"
	"releaseaworker/internal/platform"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func kubeRewriteClient(server *httptest.Server) *http.Client {
	target, _ := url.Parse(server.URL)
	base := server.Client().Transport
	return &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			clone := req.Clone(req.Context())
			clone.URL.Scheme = target.Scheme
			clone.URL.Host = target.Host
			return base.RoundTrip(clone)
		}),
	}
}

type cleanupLogger struct {
	lines []string
}

func (l *cleanupLogger) Logf(_ context.Context, format string, _ ...interface{}) {
	l.lines = append(l.lines, format)
}

func (l *cleanupLogger) Flush(_ context.Context) {}

func TestCleanupCandidatesForStrategy(t *testing.T) {
	if got := cleanupCandidatesForStrategy("api", "rolling"); len(got) != 3 {
		t.Fatalf("expected rolling cleanup candidates, got %v", got)
	}
	if got := cleanupCandidatesForStrategy("api", "canary"); len(got) != 2 {
		t.Fatalf("expected canary cleanup candidates, got %v", got)
	}
	if got := cleanupCandidatesForStrategy("api", "blue-green"); len(got) != 1 {
		t.Fatalf("expected blue-green cleanup candidates, got %v", got)
	}
	if got := cleanupCandidatesForStrategy("api", "unknown"); got != nil {
		t.Fatalf("expected nil for unknown strategy, got %v", got)
	}
}

func TestCollectDestinationHostsAndAliasChecks(t *testing.T) {
	hosts := map[string]struct{}{}
	collectDestinationHosts(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []interface{}{
				map[string]interface{}{
					"route": []interface{}{
						map[string]interface{}{
							"destination": map[string]interface{}{
								"host": "api-blue.apps.svc.cluster.local",
							},
						},
					},
				},
			},
		},
	}, hosts)

	if !isWorkloadAliasReferenced(hosts, "api-blue", "apps") {
		t.Fatalf("expected api-blue host reference")
	}
	if isWorkloadAliasReferenced(hosts, "api-green", "apps") {
		t.Fatalf("did not expect api-green host reference")
	}

	if !hostMatchesWorkloadAlias("api.apps.svc.cluster.local", "api", "apps") {
		t.Fatalf("expected host to match workload alias")
	}
	if hostMatchesWorkloadAlias("", "api", "apps") {
		t.Fatalf("empty host should not match")
	}
}

func TestNormalizeTargetCommitRef(t *testing.T) {
	if got := normalizeTargetCommitRef(" latest "); got != "" {
		t.Fatalf("expected empty for latest alias, got %q", got)
	}
	if got := normalizeTargetCommitRef("deploy-123"); got != "" {
		t.Fatalf("expected empty for deploy-* ref, got %q", got)
	}
	if got := normalizeTargetCommitRef("a1b2c3"); got != "a1b2c3" {
		t.Fatalf("expected normalized commit ref a1b2c3, got %q", got)
	}
}

func TestDockerBuildArgHelpers(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://proxy")
	t.Setenv("DOCKER_BUILD_NETWORK", "host")

	args := appendDockerBuildProxyArgs([]string{"docker", "build"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "HTTP_PROXY=http://proxy") {
		t.Fatalf("expected HTTP_PROXY build arg, got %v", args)
	}
	args = appendDockerBuildNetworkArg(args)
	joined = strings.Join(args, " ")
	if !strings.Contains(joined, "--network host") {
		t.Fatalf("expected --network host arg, got %v", args)
	}
}

func TestRegisterBuildInAPI(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/workers/builds" {
			called = true
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"serviceId":"svc-1"`) {
				t.Fatalf("unexpected request body %s", string(body))
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cfg := models.Config{ApiBaseURL: server.URL}
	registerBuildInAPI(
		context.Background(),
		server.Client(),
		cfg,
		modelsToken("token-1"),
		"svc-1",
		"abcdef",
		"abcdef",
		"gabcdef",
		"sha256:123",
		"prod",
		"repo/image:gabcdef",
	)
	if !called {
		t.Fatalf("expected build registration call")
	}
}

func TestListVirtualServiceDestinationHostsAndCanonicalTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/virtualservices"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"spec": map[string]interface{}{
							"http": []interface{}{
								map[string]interface{}{
									"route": []interface{}{
										map[string]interface{}{
											"destination": map[string]interface{}{
												"host": "api-blue.apps.svc.cluster.local",
											},
										},
									},
								},
							},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/apps/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-stable"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := kubeRewriteClient(server)
	hosts, err := listVirtualServiceDestinationHosts(context.Background(), client, "token", "apps")
	if err != nil {
		t.Fatalf("unexpected virtual service list error: %v", err)
	}
	if _, ok := hosts["api-blue.apps.svc.cluster.local"]; !ok {
		t.Fatalf("expected collected destination host")
	}

	target, err := canonicalServiceTarget(context.Background(), client, "token", "apps", "api")
	if err != nil {
		t.Fatalf("unexpected canonical target error: %v", err)
	}
	if target != "api-stable" {
		t.Fatalf("expected canonical selector api-stable, got %q", target)
	}
}

func TestCleanupUnusedStrategyWorkloads(t *testing.T) {
	deleteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/virtualservices"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"spec": map[string]interface{}{
							"http": []interface{}{
								map[string]interface{}{
									"route": []interface{}{
										map[string]interface{}{
											"destination": map[string]interface{}{
												"host": "api-blue.apps.svc.cluster.local",
											},
										},
									},
								},
							},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/apps/services/api":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"spec": map[string]interface{}{
					"selector": map[string]interface{}{"app": "api-canary"},
				},
			})
		case r.Method == http.MethodDelete:
			deleteCalls++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := kubeRewriteClient(server)
	logger := &cleanupLogger{}
	err := cleanupUnusedStrategyWorkloads(context.Background(), client, "token", "apps", "api", "rolling", logger)
	if err != nil {
		t.Fatalf("unexpected cleanup error: %v", err)
	}
	// rolling candidates: canary, blue, green
	// keep canary (canonical target), keep blue (referenced), delete only green (deployment + service)
	if deleteCalls != 2 {
		t.Fatalf("expected 2 delete calls for api-green resources, got %d", deleteCalls)
	}
}

func modelsToken(token string) *platform.TokenManager { return platform.NewTokenManager(token) }
