package rules

import (
	"reflect"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"testing"
)

func TestBuildRuleVirtualServiceAppliesEdgeHTTPPolicy(t *testing.T) {
	cfg := models.Config{
		InternalGateway: "istio-system/releasea-internal-gateway",
		InternalDomain:  "internal.example",
	}
	rule := models.RulePayload{
		ID:       "rule-1",
		Paths:    []string{"/api"},
		Methods:  []string{"GET"},
		Gateways: []string{"releasea-internal-gateway"},
		Policy: models.RulePolicyPayload{
			Action:          "allow",
			TimeoutMs:       2500,
			Retries:         1,
			SecurityHeaders: true,
			CORS:            models.RuleCORSPayload{Enabled: true, Origins: []string{"https://app.example.com"}},
		},
	}
	vs := buildRuleVirtualService(rule, models.ServicePayload{ID: "svc", Type: "service"}, "api", "apps", cfg, mapRuleGateways(rule.Gateways, cfg))
	route := shared.MapValue(shared.MapValue(vs["spec"])["http"].([]interface{})[0])
	if shared.StringValue(route, "name") != EdgeRouteName("rule-1") {
		t.Fatalf("expected route name, got %#v", route["name"])
	}
	if shared.StringValue(route, "timeout") != "2500ms" {
		t.Fatalf("expected timeout, got %#v", route["timeout"])
	}
	retries := shared.MapValue(route["retries"])
	if retries["attempts"] != 1 {
		t.Fatalf("expected one retry, got %#v", retries["attempts"])
	}
	cors := shared.MapValue(route["corsPolicy"])
	origins, _ := cors["allowOrigins"].([]interface{})
	if len(origins) != 1 || shared.StringValue(shared.MapValue(origins[0]), "exact") != "https://app.example.com" {
		t.Fatalf("expected exact CORS origin, got %#v", cors["allowOrigins"])
	}
	headers := shared.MapValue(shared.MapValue(route["headers"])["response"])
	set := shared.MapValue(headers["set"])
	if shared.StringValue(set, "x-content-type-options") != "nosniff" {
		t.Fatalf("expected security header, got %#v", set)
	}
}

func TestBuildGatewayAuthorizationPolicyForAPIKeyAndIP(t *testing.T) {
	rule := models.RulePayload{
		ID:      "rule-1",
		Paths:   []string{"/v1"},
		Methods: []string{"POST"},
		Policy: models.RulePolicyPayload{
			Action:       "allow",
			AuthMode:     "apikey",
			APIKeyHeader: "x-api-key",
			APIKeys:      []string{"gateway-key"},
			IPPolicy:     "allowlist",
			IPList:       []string{"203.0.113.0/24"},
		},
	}
	policy := buildGatewayAuthorizationPolicy(rule, models.ServicePayload{ID: "svc"}, "payments", "istio-system", []string{"payments.external.example"})
	if policy == nil {
		t.Fatal("expected gateway authorization policy")
	}
	spec := shared.MapValue(policy["spec"])
	if shared.StringValue(spec, "action") != "DENY" {
		t.Fatalf("expected DENY at the gateway, got %#v", spec["action"])
	}
	selector := shared.MapValue(shared.MapValue(spec["selector"])["matchLabels"])
	if shared.StringValue(selector, "istio") != "ingress" {
		t.Fatalf("expected ingress selector, got %#v", selector)
	}
	rules, _ := spec["rules"].([]interface{})
	if len(rules) != 2 {
		t.Fatalf("expected ip and invalid key rules, got %d", len(rules))
	}
	when, _ := shared.MapValue(rules[1])["when"].([]interface{})
	condition := shared.MapValue(when[0])
	if _, ok := condition["values"]; ok {
		t.Fatal("invalid API keys must use notValues, never an empty header value")
	}
	if shared.StringValue(condition, "key") != "request.headers[x-api-key]" {
		t.Fatalf("expected api key condition, got %#v", condition["key"])
	}
}

func TestBuildRuleVirtualServiceRejectsMissingAPIKey(t *testing.T) {
	cfg := models.Config{InternalGateway: "istio-system/releasea-internal-gateway", InternalDomain: "internal.example"}
	rule := models.RulePayload{
		ID:       "rule-1",
		Paths:    []string{"/api"},
		Methods:  []string{"GET"},
		Gateways: []string{"releasea-internal-gateway"},
		Policy: models.RulePolicyPayload{
			Action:          "allow",
			AuthMode:        "apikey",
			APIKeyHeader:    "x-api-key",
			APIKeys:         []string{"gateway-demo-key"},
			SecurityHeaders: true,
			TimeoutMs:       1500,
			Retries:         2,
		},
	}
	vs := buildRuleVirtualService(rule, models.ServicePayload{ID: "svc", Type: "service"}, "go-service", "apps", cfg, mapRuleGateways(rule.Gateways, cfg))
	routes, _ := shared.MapValue(vs["spec"])["http"].([]interface{})
	if len(routes) != 2 {
		t.Fatalf("expected allow route and unauthorized fallback, got %d", len(routes))
	}
	allow := shared.MapValue(routes[0])
	matches, _ := allow["match"].([]interface{})
	headers := shared.MapValue(shared.MapValue(matches[0])["headers"])
	if shared.StringValue(shared.MapValue(headers["x-api-key"]), "exact") != "gateway-demo-key" {
		t.Fatalf("expected exact api key match, got %#v", headers)
	}
	fallback := shared.MapValue(routes[1])
	direct := shared.MapValue(fallback["directResponse"])
	if direct["status"] != 401 {
		t.Fatalf("expected 401 for a missing key, got %#v", direct["status"])
	}
}

func TestBuildRouteEdgeFilterEnablesRateLimitOnNamedRoute(t *testing.T) {
	rule := models.RulePayload{
		ID: "rule-1",
		Policy: models.RulePolicyPayload{
			RateLimit:    models.RuleRateLimitPayload{Requests: 40, WindowSeconds: 30},
			MaxBodyBytes: 4096,
		},
	}
	filter := buildRouteEdgeFilter(rule, models.ServicePayload{ID: "svc"}, "payments", "istio-system")
	spec := shared.MapValue(filter["spec"])
	patches, _ := spec["configPatches"].([]interface{})
	if len(patches) != 1 {
		t.Fatalf("expected one route patch, got %d", len(patches))
	}
	match := shared.MapValue(shared.MapValue(patches[0])["match"])
	route := shared.MapValue(shared.MapValue(shared.MapValue(match["routeConfiguration"])["vhost"])["route"])
	if shared.StringValue(route, "name") != EdgeRouteName("rule-1") {
		t.Fatalf("expected named route match, got %#v", route["name"])
	}
	value := shared.MapValue(shared.MapValue(shared.MapValue(patches[0])["patch"])["value"])
	typed := shared.MapValue(value["typed_per_filter_config"])
	if _, ok := typed["envoy.filters.http.local_ratelimit"]; !ok {
		t.Fatal("expected local rate limit config")
	}
	if _, ok := typed["envoy.filters.http.lua"]; !ok {
		t.Fatal("expected max body lua config")
	}
}

func TestBuildEdgeDestinationRuleIncludesCircuitBreakerAndMTLS(t *testing.T) {
	rule := models.RulePayload{
		ID: "rule-1",
		Policy: models.RulePolicyPayload{
			AuthMode:       "mtls",
			CircuitBreaker: models.RuleCircuitBreakerPayload{ConsecutiveErrors: 4},
		},
	}
	destination := buildEdgeDestinationRule(rule, models.ServicePayload{ID: "svc"}, "payments", "apps")
	traffic := shared.MapValue(shared.MapValue(destination["spec"])["trafficPolicy"])
	outlier := shared.MapValue(traffic["outlierDetection"])
	if outlier["consecutive5xxErrors"] != 4 {
		t.Fatalf("expected circuit breaker, got %#v", outlier)
	}
	tls := shared.MapValue(traffic["tls"])
	if shared.StringValue(tls, "mode") != "ISTIO_MUTUAL" {
		t.Fatalf("expected mesh mTLS, got %#v", tls["mode"])
	}
	if !reflect.DeepEqual(RequiresGatewayAuth(rule), false) {
		t.Fatal("mTLS alone does not add a gateway authorization policy")
	}
}
