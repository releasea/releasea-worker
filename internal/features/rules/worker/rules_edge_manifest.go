package rules

import (
	"fmt"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
)

const gatewayEdgeFilterName = "releasea-gateway-edge-filters"

func GatewayNamespace(cfg models.Config) string {
	for _, ref := range []string{cfg.ExternalGateway, cfg.InternalGateway} {
		ref = strings.TrimSpace(ref)
		if slash := strings.Index(ref, "/"); slash > 0 {
			return ref[:slash]
		}
	}
	return "istio-system"
}

func EdgeRouteName(ruleID string) string {
	base := shared.ToKubeName(ruleID)
	if base == "" {
		base = "rule"
	}
	name := "edge-" + base
	if len(name) <= 63 {
		return name
	}
	return strings.Trim(name[:63], "-")
}

func GatewayEdgePolicyName(serviceName, ruleID string) string {
	return buildSecurityResourceName(serviceName, ruleID, "gw")
}

func GatewayJWTName(serviceName, ruleID string) string {
	return buildSecurityResourceName(serviceName, ruleID, "gjwt")
}

func EdgeRateLimitName(serviceName, ruleID string) string {
	return buildSecurityResourceName(serviceName, ruleID, "rl")
}

func EdgeDestinationRuleName(serviceName, ruleID string) string {
	return buildSecurityResourceName(serviceName, ruleID, "dr")
}

func EdgePeerAuthenticationName(serviceName, ruleID string) string {
	return buildSecurityResourceName(serviceName, ruleID, "mtls")
}

func EdgeNetworkPolicyName(serviceName, ruleID string) string {
	return buildSecurityResourceName(serviceName, ruleID, "net")
}

func RequiresGatewayAuth(rule models.RulePayload) bool {
	if RuleAction(rule) == "deny" {
		return true
	}
	switch RuleAuthMode(rule) {
	case "jwt", "apikey":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(rule.Policy.IPPolicy)) {
	case "allowlist", "blocklist":
		return true
	}
	return len(normalizeScopes(rule.Policy.RequiredScopes)) > 0
}

func applyEdgeHTTPPolicy(httpRoute map[string]interface{}, rule models.RulePayload) {
	if rule.Policy.TimeoutMs > 0 {
		httpRoute["timeout"] = fmt.Sprintf("%dms", rule.Policy.TimeoutMs)
	}
	if rule.Policy.Retries > 0 && RuleAction(rule) != "deny" && httpRoute["directResponse"] == nil {
		perTry := rule.Policy.TimeoutMs
		if perTry <= 0 {
			perTry = 1000
		}
		httpRoute["retries"] = map[string]interface{}{
			"attempts":      rule.Policy.Retries,
			"perTryTimeout": fmt.Sprintf("%dms", perTry),
			"retryOn":       "gateway-error,connect-failure,refused-stream,reset",
		}
	}
	if cors := buildCORSPolicy(rule); cors != nil {
		httpRoute["corsPolicy"] = cors
	}
	if rule.Policy.SecurityHeaders {
		httpRoute["headers"] = map[string]interface{}{
			"response": map[string]interface{}{
				"set": map[string]interface{}{
					"x-content-type-options":    "nosniff",
					"x-frame-options":           "DENY",
					"referrer-policy":           "no-referrer",
					"x-xss-protection":          "0",
					"strict-transport-security": "max-age=31536000; includeSubDomains",
				},
			},
		}
	}
}

func buildCORSPolicy(rule models.RulePayload) map[string]interface{} {
	if !rule.Policy.CORS.Enabled || RuleAction(rule) == "deny" {
		return nil
	}
	origins := shared.UniqueStrings(rule.Policy.CORS.Origins)
	allowOrigins := make([]interface{}, 0, len(origins))
	if len(origins) == 0 || (len(origins) == 1 && origins[0] == "*") {
		allowOrigins = append(allowOrigins, map[string]interface{}{"regex": ".*"})
	} else {
		for _, origin := range origins {
			allowOrigins = append(allowOrigins, map[string]interface{}{"exact": origin})
		}
	}
	methods := normalizePolicyMethods(rule.Policy.CORS.Methods)
	if len(methods) == 0 {
		methods = normalizePolicyMethods(rule.Methods)
	}
	headers := normalizeHeaderNames(rule.Policy.CORS.Headers)
	if len(headers) == 0 {
		headers = []string{"authorization", "content-type", "x-requested-with", "x-api-key"}
	}
	maxAge := rule.Policy.CORS.MaxAgeSeconds
	if maxAge <= 0 {
		maxAge = 600
	}
	return map[string]interface{}{
		"allowOrigins": allowOrigins,
		"allowMethods": methods,
		"allowHeaders": headers,
		"maxAge":       fmt.Sprintf("%ds", maxAge),
	}
}

func buildGatewayAuthorizationPolicy(rule models.RulePayload, service models.ServicePayload, serviceName, gatewayNamespace string, hosts []string) map[string]interface{} {
	rules := buildGatewayDenyRules(rule, hosts)
	if len(rules) == 0 {
		return nil
	}
	return map[string]interface{}{
		"apiVersion": "security.istio.io/v1beta1",
		"kind":       "AuthorizationPolicy",
		"metadata":   edgeMetadata(GatewayEdgePolicyName(serviceName, rule.ID), gatewayNamespace, serviceName, rule, service),
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"istio": "ingress"}},
			"action":   "DENY",
			"rules":    rules,
		},
	}
}

func buildGatewayDenyRules(rule models.RulePayload, hosts []string) []interface{} {
	operation := edgeOperation(rule, hosts)
	if RuleAction(rule) == "deny" {
		return []interface{}{denyEntry(operation, nil, nil)}
	}
	rules := make([]interface{}, 0)
	switch strings.ToLower(strings.TrimSpace(rule.Policy.IPPolicy)) {
	case "allowlist":
		if cidrs := shared.UniqueStrings(rule.Policy.IPList); len(cidrs) > 0 {
			rules = append(rules, denyEntry(operation, map[string]interface{}{"notRemoteIpBlocks": cidrs}, nil))
		}
	case "blocklist":
		if cidrs := shared.UniqueStrings(rule.Policy.IPList); len(cidrs) > 0 {
			rules = append(rules, denyEntry(operation, map[string]interface{}{"remoteIpBlocks": cidrs}, nil))
		}
	}
	if RuleAuthMode(rule) == "jwt" {
		rules = append(rules, denyEntry(operation, map[string]interface{}{"notRequestPrincipals": []string{"*"}}, nil))
		for _, scope := range normalizeScopes(rule.Policy.RequiredScopes) {
			rules = append(rules, denyEntry(operation, nil, []interface{}{
				map[string]interface{}{
					"key":       "request.auth.claims[scope]",
					"notValues": []string{scope},
				},
			}))
		}
	}
	if header, keys := apiKeyConstraint(rule); header != "" && len(keys) > 0 {
		rules = append(rules, denyEntry(operation, nil, []interface{}{
			map[string]interface{}{
				"key":       "request.headers[" + header + "]",
				"notValues": keys,
			},
		}))
	}
	return rules
}

func apiKeyConstraint(rule models.RulePayload) (string, []string) {
	if RuleAction(rule) == "deny" || RuleAuthMode(rule) != "apikey" {
		return "", nil
	}
	header := strings.ToLower(strings.TrimSpace(rule.Policy.APIKeyHeader))
	if header == "" {
		header = "x-api-key"
	}
	return header, shared.UniqueStrings(rule.Policy.APIKeys)
}

func edgePositiveMatches(rule models.RulePayload) ([]interface{}, bool) {
	header, keys := apiKeyConstraint(rule)
	required := normalizeHeaderNames(rule.Policy.RequiredHeaders)
	if header == "" && len(required) == 0 {
		return nil, false
	}
	base := buildRuleHTTPMatches(rule.Paths, rule.Methods)
	if len(base) == 0 {
		base = []interface{}{map[string]interface{}{}}
	}
	requiredHeaders := map[string]interface{}{}
	for _, name := range required {
		requiredHeaders[name] = map[string]interface{}{"regex": ".+"}
	}
	if len(keys) == 0 {
		if header != "" {
			return nil, true
		}
		return withMatchHeaders(base, requiredHeaders), true
	}
	matches := make([]interface{}, 0, len(base)*len(keys))
	for _, key := range keys {
		headers := map[string]interface{}{}
		for name, value := range requiredHeaders {
			headers[name] = value
		}
		headers[header] = map[string]interface{}{"exact": key}
		matches = append(matches, withMatchHeaders(base, headers)...)
	}
	return matches, true
}

func withMatchHeaders(matches []interface{}, headers map[string]interface{}) []interface{} {
	out := make([]interface{}, 0, len(matches))
	for _, item := range matches {
		match := map[string]interface{}{}
		for key, value := range shared.MapValue(item) {
			match[key] = value
		}
		match["headers"] = headers
		out = append(out, match)
	}
	return out
}

func buildGatewayJWTRequestAuthentication(rule models.RulePayload, service models.ServicePayload, serviceName, gatewayNamespace string) map[string]interface{} {
	resource := buildJWTRequestAuthentication(rule, service, serviceName, gatewayNamespace)
	meta := shared.MapValue(resource["metadata"])
	meta["name"] = GatewayJWTName(serviceName, rule.ID)
	meta["namespace"] = gatewayNamespace
	spec := shared.MapValue(resource["spec"])
	spec["selector"] = map[string]interface{}{"matchLabels": map[string]interface{}{"istio": "ingress"}}
	return resource
}

func buildEdgeDestinationRule(rule models.RulePayload, service models.ServicePayload, serviceName, namespace string) map[string]interface{} {
	if RuleAction(rule) == "deny" {
		return nil
	}
	traffic := map[string]interface{}{}
	if errors := rule.Policy.CircuitBreaker.ConsecutiveErrors; errors > 0 {
		interval := rule.Policy.CircuitBreaker.IntervalSeconds
		if interval <= 0 {
			interval = 10
		}
		base := rule.Policy.CircuitBreaker.BaseEjectionSeconds
		if base <= 0 {
			base = 30
		}
		maxEject := rule.Policy.CircuitBreaker.MaxEjectionPercent
		if maxEject <= 0 || maxEject > 100 {
			maxEject = 50
		}
		traffic["outlierDetection"] = map[string]interface{}{
			"consecutive5xxErrors": errors,
			"interval":             fmt.Sprintf("%ds", interval),
			"baseEjectionTime":     fmt.Sprintf("%ds", base),
			"maxEjectionPercent":   maxEject,
		}
		traffic["connectionPool"] = map[string]interface{}{
			"http": map[string]interface{}{
				"http1MaxPendingRequests":  64,
				"maxRequestsPerConnection": 0,
			},
		}
	}
	if RuleAuthMode(rule) == "mtls" {
		traffic["tls"] = map[string]interface{}{"mode": "ISTIO_MUTUAL"}
	}
	if len(traffic) == 0 {
		return nil
	}
	return map[string]interface{}{
		"apiVersion": "networking.istio.io/v1beta1",
		"kind":       "DestinationRule",
		"metadata":   edgeMetadata(EdgeDestinationRuleName(serviceName, rule.ID), namespace, serviceName, rule, service),
		"spec": map[string]interface{}{
			"host":          fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
			"trafficPolicy": traffic,
		},
	}
}

func buildEdgePeerAuthentication(rule models.RulePayload, service models.ServicePayload, serviceName, namespace string) map[string]interface{} {
	if RuleAction(rule) == "deny" || RuleAuthMode(rule) != "mtls" {
		return nil
	}
	return map[string]interface{}{
		"apiVersion": "security.istio.io/v1beta1",
		"kind":       "PeerAuthentication",
		"metadata":   edgeMetadata(EdgePeerAuthenticationName(serviceName, rule.ID), namespace, serviceName, rule, service),
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": serviceName}},
			"mtls":     map[string]interface{}{"mode": "STRICT"},
		},
	}
}

func buildEdgeNetworkPolicy(rule models.RulePayload, service models.ServicePayload, serviceName, namespace, gatewayNamespace string) map[string]interface{} {
	if RuleAction(rule) == "deny" || !rule.Policy.LockNetwork {
		return nil
	}
	if strings.TrimSpace(gatewayNamespace) == "" {
		gatewayNamespace = "istio-system"
	}
	return map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   edgeMetadata(EdgeNetworkPolicyName(serviceName, rule.ID), namespace, serviceName, rule, service),
		"spec": map[string]interface{}{
			"podSelector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": serviceName}},
			"policyTypes": []string{"Ingress"},
			"ingress": []interface{}{
				map[string]interface{}{
					"from": []interface{}{
						map[string]interface{}{
							"namespaceSelector": map[string]interface{}{
								"matchLabels": map[string]interface{}{"kubernetes.io/metadata.name": gatewayNamespace},
							},
						},
						map[string]interface{}{"podSelector": map[string]interface{}{}},
					},
				},
			},
		},
	}
}

func buildGlobalEdgeFilter(gatewayNamespace string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "networking.istio.io/v1alpha3",
		"kind":       "EnvoyFilter",
		"metadata": map[string]interface{}{
			"name":      gatewayEdgeFilterName,
			"namespace": gatewayNamespace,
			"labels":    map[string]interface{}{"releasea.edge": "gateway"},
		},
		"spec": map[string]interface{}{
			"workloadSelector": map[string]interface{}{"labels": map[string]interface{}{"istio": "ingress"}},
			"configPatches": []interface{}{
				edgeHTTPFilterPatch("envoy.filters.http.local_ratelimit", map[string]interface{}{
					"@type":       "type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit",
					"stat_prefix": "releasea_edge_rate_limit",
					"token_bucket": map[string]interface{}{
						"max_tokens":      1,
						"tokens_per_fill": 1,
						"fill_interval":   "60s",
					},
					"filter_enabled":  edgePercent(0),
					"filter_enforced": edgePercent(0),
				}),
				edgeHTTPFilterPatch("envoy.filters.http.lua", map[string]interface{}{
					"@type":       "type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua",
					"inline_code": "function envoy_on_request(handle)\nend\n",
				}),
			},
		},
	}
}

func buildRouteEdgeFilter(rule models.RulePayload, service models.ServicePayload, serviceName, gatewayNamespace string) map[string]interface{} {
	if RuleAction(rule) == "deny" || (rule.Policy.RateLimit.Requests <= 0 && rule.Policy.MaxBodyBytes <= 0) {
		return nil
	}
	typed := map[string]interface{}{}
	if rule.Policy.RateLimit.Requests > 0 {
		window := rule.Policy.RateLimit.WindowSeconds
		if window <= 0 {
			window = 60
		}
		typed["envoy.filters.http.local_ratelimit"] = map[string]interface{}{
			"@type":       "type.googleapis.com/envoy.extensions.filters.http.local_ratelimit.v3.LocalRateLimit",
			"stat_prefix": "releasea_edge_" + strings.ReplaceAll(shared.ToKubeName(rule.ID), "-", "_"),
			"token_bucket": map[string]interface{}{
				"max_tokens":      rule.Policy.RateLimit.Requests,
				"tokens_per_fill": rule.Policy.RateLimit.Requests,
				"fill_interval":   fmt.Sprintf("%ds", window),
			},
			"filter_enabled":  edgePercent(100),
			"filter_enforced": edgePercent(100),
		}
	}
	if rule.Policy.MaxBodyBytes > 0 {
		typed["envoy.filters.http.lua"] = map[string]interface{}{
			"@type": "type.googleapis.com/envoy.extensions.filters.http.lua.v3.LuaPerRoute",
			"source_code": map[string]interface{}{
				"inline_string": fmt.Sprintf(`function envoy_on_request(handle)
  local length = handle:headers():get("content-length")
  local size = tonumber(length)
  if size ~= nil and size > %d then
    handle:respond({[":status"] = "413", ["content-type"] = "text/plain"}, "payload too large")
  end
end
`, rule.Policy.MaxBodyBytes),
			},
		}
	}
	return map[string]interface{}{
		"apiVersion": "networking.istio.io/v1alpha3",
		"kind":       "EnvoyFilter",
		"metadata":   edgeMetadata(EdgeRateLimitName(serviceName, rule.ID), gatewayNamespace, serviceName, rule, service),
		"spec": map[string]interface{}{
			"workloadSelector": map[string]interface{}{"labels": map[string]interface{}{"istio": "ingress"}},
			"configPatches": []interface{}{
				map[string]interface{}{
					"applyTo": "HTTP_ROUTE",
					"match": map[string]interface{}{
						"context": "GATEWAY",
						"routeConfiguration": map[string]interface{}{
							"vhost": map[string]interface{}{
								"route": map[string]interface{}{"name": EdgeRouteName(rule.ID)},
							},
						},
					},
					"patch": map[string]interface{}{
						"operation": "MERGE",
						"value": map[string]interface{}{
							"typed_per_filter_config": typed,
						},
					},
				},
			},
		},
	}
}

func edgeHTTPFilterPatch(name string, typed map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"applyTo": "HTTP_FILTER",
		"match": map[string]interface{}{
			"context": "GATEWAY",
			"listener": map[string]interface{}{
				"filterChain": map[string]interface{}{
					"filter": map[string]interface{}{
						"name": "envoy.filters.network.http_connection_manager",
						"subFilter": map[string]interface{}{
							"name": "envoy.filters.http.router",
						},
					},
				},
			},
		},
		"patch": map[string]interface{}{
			"operation": "INSERT_BEFORE",
			"value": map[string]interface{}{
				"name":         name,
				"typed_config": typed,
			},
		},
	}
}

func edgePercent(numerator int) map[string]interface{} {
	return map[string]interface{}{
		"default_value": map[string]interface{}{
			"numerator":   numerator,
			"denominator": "HUNDRED",
		},
	}
}

func edgeOperation(rule models.RulePayload, hosts []string) map[string]interface{} {
	operation := map[string]interface{}{}
	if methods := normalizePolicyMethods(rule.Methods); len(methods) > 0 {
		operation["methods"] = methods
	}
	if paths := expandDenyPolicyPaths(rule.Paths); len(paths) > 0 {
		operation["paths"] = paths
	}
	if cleaned := shared.UniqueStrings(hosts); len(cleaned) > 0 {
		operation["hosts"] = cleaned
	}
	return operation
}

func denyEntry(operation map[string]interface{}, source map[string]interface{}, when []interface{}) map[string]interface{} {
	entry := map[string]interface{}{}
	if len(operation) > 0 {
		entry["to"] = []interface{}{map[string]interface{}{"operation": operation}}
	}
	if len(source) > 0 {
		entry["from"] = []interface{}{map[string]interface{}{"source": source}}
	}
	if len(when) > 0 {
		entry["when"] = when
	}
	return entry
}

func edgeMetadata(name, namespace, serviceName string, rule models.RulePayload, service models.ServicePayload) map[string]interface{} {
	return map[string]interface{}{
		"name":      name,
		"namespace": namespace,
		"labels": map[string]interface{}{
			"app":              serviceName,
			"releasea.rule":    rule.ID,
			"releasea.service": service.ID,
			"releasea.edge":    "true",
		},
	}
}

func normalizeHeaderNames(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		header := strings.ToLower(strings.TrimSpace(value))
		if header == "" {
			continue
		}
		out = append(out, header)
	}
	return shared.UniqueStrings(out)
}

func normalizeScopes(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		scope := strings.TrimSpace(value)
		if scope == "" {
			continue
		}
		out = append(out, scope)
	}
	return shared.UniqueStrings(out)
}
