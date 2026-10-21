package rules

import (
	"reflect"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
	"testing"
)

func TestRuleAction(t *testing.T) {
	if got := RuleAction(models.RulePayload{}); got != "allow" {
		t.Fatalf("expected default allow, got %q", got)
	}
	if got := RuleAction(models.RulePayload{Policy: models.RulePolicyPayload{Action: "DENY"}}); got != "deny" {
		t.Fatalf("expected deny action, got %q", got)
	}
}

func TestRuleAuthMode(t *testing.T) {
	if got := RuleAuthMode(models.RulePayload{}); got != "public" {
		t.Fatalf("expected default public mode, got %q", got)
	}
	if got := RuleAuthMode(models.RulePayload{Policy: models.RulePolicyPayload{AuthMode: "JWT"}}); got != "jwt" {
		t.Fatalf("expected jwt mode, got %q", got)
	}
}

func TestBuildDenyPolicyName(t *testing.T) {
	got := BuildDenyPolicyName("my-service", "rule 01", "")
	if got != "my-service-deny-rule-01" {
		t.Fatalf("unexpected policy name: %q", got)
	}

	longName := BuildDenyPolicyName(strings.Repeat("service", 20), strings.Repeat("rule", 20), "")
	if len(longName) > 63 {
		t.Fatalf("policy name must be <=63 chars, got %d", len(longName))
	}
}

func TestExpandDenyPolicyPaths(t *testing.T) {
	got := expandDenyPolicyPaths([]string{"", "/", "/api", "/api/*", "/api"})
	want := []string{"/", "/*", "/api", "/api/*"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestNormalizePolicyMethods(t *testing.T) {
	got := normalizePolicyMethods([]string{" get ", "", "POST", "get"})
	want := []string{"GET", "POST"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestBuildRuleHTTPMatches(t *testing.T) {
	matches := buildRuleHTTPMatches([]string{"/api"}, []string{"get", "POST"})
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	first, ok := matches[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected map match")
	}
	method := shared.MapValue(first["method"])
	if shared.StringValue(method, "exact") != "GET" {
		t.Fatalf("expected upper method GET, got %q", shared.StringValue(method, "exact"))
	}
}

func TestBuildStrategyDestinations(t *testing.T) {
	canary := buildStrategyDestinations(models.ServicePayload{
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type:          "canary",
			CanaryPercent: 20,
		},
	}, "api", "apps", 8080)
	if len(canary) != 2 {
		t.Fatalf("expected canary 2 destinations, got %d", len(canary))
	}

	blueGreen := buildStrategyDestinations(models.ServicePayload{
		DeploymentStrategy: models.DeploymentStrategyConfig{
			Type:             "blue-green",
			BlueGreenPrimary: "green",
		},
	}, "api", "apps", 8080)
	if len(blueGreen) != 1 {
		t.Fatalf("expected blue-green 1 destination, got %d", len(blueGreen))
	}
	item, _ := blueGreen[0].(map[string]interface{})
	dest := shared.MapValue(item["destination"])
	host := shared.StringValue(dest, "host")
	if !strings.Contains(host, "api-green.apps.svc.cluster.local") {
		t.Fatalf("expected host api-green..., got %q", host)
	}
}

func TestResolveRuleHostsAndMapGateways(t *testing.T) {
	cfg := models.Config{
		InternalGateway: "istio-system/releasea-internal-gateway",
		ExternalGateway: "istio-system/releasea-external-gateway",
		InternalDomain:  "internal.example",
		ExternalDomain:  "external.example",
	}
	gateways := mapRuleGateways([]string{"releasea-internal-gateway", "mesh", "releasea-external-gateway"}, cfg)
	if want := []string{cfg.InternalGateway, cfg.ExternalGateway}; !reflect.DeepEqual(gateways, want) {
		t.Fatalf("expected mapped gateways %v, got %v", want, gateways)
	}

	hosts := resolveRuleHosts(nil, gateways, "api", "releasea-apps-development", cfg)
	if len(hosts) < 2 {
		t.Fatalf("expected generated internal/external hosts, got %v", hosts)
	}
	if !shared.HasHostSuffix(hosts, cfg.InternalDomain) || !containsHost(hosts, "api-dev.internal.example") {
		t.Fatalf("expected development internal host, got %v", hosts)
	}
	if !shared.HasHostSuffix(hosts, cfg.ExternalDomain) || !containsHost(hosts, "api-dev.external.example") {
		t.Fatalf("expected development external host, got %v", hosts)
	}
	stagingHosts := resolveRuleHosts(nil, gateways, "api", "releasea-apps-staging", cfg)
	if !containsHost(stagingHosts, "api-staging.internal.example") {
		t.Fatalf("expected staging host, got %v", stagingHosts)
	}
	prodHosts := resolveRuleHosts(nil, gateways, "api", "releasea-apps-production", cfg)
	if !containsHost(prodHosts, "api-prod.internal.example") {
		t.Fatalf("expected production host, got %v", prodHosts)
	}
}

func TestRuleVirtualServiceName(t *testing.T) {
	if got := RuleVirtualServiceName("api", "api-rule-1"); got != "api-rule-1" {
		t.Fatalf("unexpected vs name %q", got)
	}
	if got := RuleVirtualServiceName("api", "rule 2"); got != "api-rule-2" {
		t.Fatalf("unexpected normalized vs name %q", got)
	}
}

func TestBuildRuleVirtualServiceDeny(t *testing.T) {
	cfg := models.Config{
		InternalGateway: "istio-system/releasea-internal-gateway",
		InternalDomain:  "internal.example",
	}
	rule := models.RulePayload{
		ID:       "r1",
		Name:     "deny-api",
		Gateways: []string{"releasea-internal-gateway"},
		Paths:    []string{"/api"},
		Methods:  []string{"GET"},
		Policy: models.RulePolicyPayload{
			Action: "deny",
		},
	}
	service := models.ServicePayload{
		ID:   "s1",
		Type: "service",
	}
	vs := buildRuleVirtualService(rule, service, "api", "apps", cfg, mapRuleGateways(rule.Gateways, cfg))
	spec := shared.MapValue(vs["spec"])
	httpRoutes, ok := spec["http"].([]interface{})
	if !ok || len(httpRoutes) != 1 {
		t.Fatalf("expected one http route")
	}
	route, _ := httpRoutes[0].(map[string]interface{})
	directResponse := shared.MapValue(route["directResponse"])
	if directResponse["status"] != 403 {
		t.Fatalf("expected deny direct response 403, got %#v", directResponse["status"])
	}
	if _, exists := route["route"]; exists {
		t.Fatalf("deny route must not include traffic route")
	}
}

func TestBuildJWTAccessResources(t *testing.T) {
	rule := models.RulePayload{
		ID:      "rule-1",
		Paths:   []string{"/api"},
		Methods: []string{"get"},
		Policy: models.RulePolicyPayload{
			AuthMode:  "jwt",
			JWTIssuer: "https://id.example.com",
			JWKSURI:   "https://id.example.com/.well-known/jwks.json",
			Audiences: []string{"payments-api"},
		},
	}
	service := models.ServicePayload{ID: "svc-1"}

	authentication := buildJWTRequestAuthentication(rule, service, "payments", "apps")
	if authentication["kind"] != "RequestAuthentication" {
		t.Fatalf("expected RequestAuthentication, got %#v", authentication["kind"])
	}
	authSpec := shared.MapValue(authentication["spec"])
	jwtRules, ok := authSpec["jwtRules"].([]interface{})
	if !ok || len(jwtRules) != 1 {
		t.Fatalf("expected one JWT rule, got %#v", authSpec["jwtRules"])
	}
	jwtRule := shared.MapValue(jwtRules[0])
	if shared.StringValue(jwtRule, "issuer") != rule.Policy.JWTIssuer {
		t.Fatalf("unexpected issuer: %#v", jwtRule["issuer"])
	}

	authorization := buildJWTAuthorizationPolicy(rule, service, "payments", "apps")
	if authorization["kind"] != "AuthorizationPolicy" {
		t.Fatalf("expected AuthorizationPolicy, got %#v", authorization["kind"])
	}
	authorizationSpec := shared.MapValue(authorization["spec"])
	if shared.StringValue(authorizationSpec, "action") != "DENY" {
		t.Fatalf("expected missing-principal DENY policy, got %#v", authorizationSpec["action"])
	}
	rules, ok := authorizationSpec["rules"].([]interface{})
	if !ok || len(rules) != 1 {
		t.Fatalf("expected one authorization rule, got %#v", authorizationSpec["rules"])
	}
	entry := shared.MapValue(rules[0])
	from, ok := entry["from"].([]interface{})
	if !ok || len(from) != 1 {
		t.Fatalf("expected source constraint, got %#v", entry["from"])
	}
	source := shared.MapValue(shared.MapValue(from[0])["source"])
	principals, ok := source["notRequestPrincipals"].([]string)
	if !ok || !reflect.DeepEqual(principals, []string{"*"}) {
		t.Fatalf("expected unauthenticated-principal match, got %#v", source["notRequestPrincipals"])
	}
}

func containsHost(hosts []string, want string) bool {
	for _, host := range hosts {
		if host == want {
			return true
		}
	}
	return false
}
