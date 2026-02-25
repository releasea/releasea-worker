package rules

import (
	"fmt"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
)

func buildRuleVirtualService(rule models.RulePayload, service models.ServicePayload, serviceName, namespace string, cfg models.Config, gateways []string) map[string]interface{} {
	port := rule.Port
	if port <= 0 {
		if service.Port > 0 {
			port = service.Port
		} else {
			port = 80
		}
	}
	hosts := resolveRuleHosts(rule.Hosts, gateways, serviceName, namespace, cfg)
	vsName := RuleVirtualServiceName(serviceName, rule.ID)
	destinationHost := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
	destinationPort := port
	destinations := []interface{}{}
	if strings.EqualFold(service.Type, "static-site") {
		if staticHost := staticNginxHost(cfg); staticHost != "" {
			destinationHost = staticHost
			destinationPort = 80
		}
		destinations = []interface{}{buildRouteDestination(destinationHost, destinationPort, 100)}
	} else {
		destinations = buildStrategyDestinations(service, serviceName, namespace, destinationPort)
	}
	if len(destinations) == 0 {
		destinations = []interface{}{buildRouteDestination(destinationHost, destinationPort, 100)}
	}

	spec := map[string]interface{}{
		"hosts":    hosts,
		"gateways": gateways,
	}

	protocol := strings.ToLower(strings.TrimSpace(rule.Protocol))
	isDeny := RuleAction(rule) == "deny"
	if protocol == "tcp" {
		tcpRoute := map[string]interface{}{
			"route": destinations,
		}
		if port > 0 {
			tcpRoute["match"] = []interface{}{
				map[string]interface{}{"port": port},
			}
		}
		spec["tcp"] = []interface{}{tcpRoute}
	} else {
		httpRoute := map[string]interface{}{}
		if matches := buildRuleHTTPMatches(rule.Paths, rule.Methods); len(matches) > 0 {
			httpRoute["match"] = matches
		}
		if isDeny {
			httpRoute["directResponse"] = map[string]interface{}{
				"status": 403,
			}
		} else {
			httpRoute["route"] = destinations
		}
		spec["http"] = []interface{}{httpRoute}
	}

	return map[string]interface{}{
		"apiVersion": "networking.istio.io/v1beta1",
		"kind":       "VirtualService",
		"metadata": map[string]interface{}{
			"name":      vsName,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app":              serviceName,
				"releasea.rule":    rule.ID,
				"releasea.service": service.ID,
			},
		},
		"spec": spec,
	}
}

func buildStrategyDestinations(service models.ServicePayload, serviceName, namespace string, port int) []interface{} {
	strategyType := strings.ToLower(strings.TrimSpace(service.DeploymentStrategy.Type))
	stableHost := fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace)
	switch strategyType {
	case "canary":
		canaryPercent := service.DeploymentStrategy.CanaryPercent
		if canaryPercent <= 0 {
			// Promoted: 100% traffic to stable
			return []interface{}{
				buildRouteDestination(stableHost, port, 100),
			}
		}
		if canaryPercent > 50 {
			canaryPercent = 50
		}
		stablePercent := 100 - canaryPercent
		canaryHost := fmt.Sprintf("%s-canary.%s.svc.cluster.local", serviceName, namespace)
		return []interface{}{
			buildRouteDestination(stableHost, port, stablePercent),
			buildRouteDestination(canaryHost, port, canaryPercent),
		}
	case "blue-green":
		primaryColor, _ := shared.ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
		primaryHost := fmt.Sprintf("%s-%s.%s.svc.cluster.local", serviceName, primaryColor, namespace)
		return []interface{}{
			buildRouteDestination(primaryHost, port, 100),
		}
	default:
		return []interface{}{
			buildRouteDestination(stableHost, port, 100),
		}
	}
}

func buildRouteDestination(host string, port int, weight int) map[string]interface{} {
	out := map[string]interface{}{
		"destination": map[string]interface{}{
			"host": host,
			"port": map[string]interface{}{
				"number": port,
			},
		},
	}
	if weight > 0 {
		out["weight"] = weight
	}
	return out
}

func RuleAction(rule models.RulePayload) string {
	action := strings.ToLower(strings.TrimSpace(rule.Policy.Action))
	if action == "" {
		return "allow"
	}
	return action
}

func BuildDenyPolicyName(serviceName, ruleName, ruleID string) string {
	baseService := shared.ToKubeName(serviceName)
	if baseService == "" {
		baseService = "service"
	}
	baseRule := shared.ToKubeName(ruleName)
	if baseRule == "" {
		baseRule = shared.ToKubeName(ruleID)
	}
	if baseRule == "" {
		baseRule = "rule"
	}
	name := fmt.Sprintf("%s-deny-%s", baseService, baseRule)
	if len(name) <= 63 {
		return name
	}
	return strings.Trim(name[:63], "-")
}

func buildDenyAuthorizationPolicy(rule models.RulePayload, service models.ServicePayload, serviceName, namespace, policyName string) map[string]interface{} {
	paths := expandDenyPolicyPaths(rule.Paths)
	methods := normalizePolicyMethods(rule.Methods)
	operation := map[string]interface{}{}
	if len(methods) > 0 {
		operation["methods"] = methods
	}
	if len(paths) > 0 {
		operation["paths"] = paths
	}
	ruleEntry := map[string]interface{}{}
	if len(operation) > 0 {
		ruleEntry["to"] = []interface{}{
			map[string]interface{}{
				"operation": operation,
			},
		}
	}

	return map[string]interface{}{
		"apiVersion": "security.istio.io/v1beta1",
		"kind":       "AuthorizationPolicy",
		"metadata": map[string]interface{}{
			"name":      policyName,
			"namespace": namespace,
			"labels": map[string]interface{}{
				"app":              serviceName,
				"releasea.rule":    rule.ID,
				"releasea.service": service.ID,
			},
		},
		"spec": map[string]interface{}{
			"selector": map[string]interface{}{
				"matchLabels": map[string]interface{}{
					"app": serviceName,
				},
			},
			"action": "DENY",
			"rules":  []interface{}{ruleEntry},
		},
	}
}

func expandDenyPolicyPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0)
	for _, raw := range paths {
		path := normalizeRulePath(raw)
		if path == "" {
			continue
		}
		if path == "/" {
			out = append(out, "/", "/*")
			continue
		}
		out = append(out, path)
		if !strings.HasSuffix(path, "/*") {
			out = append(out, path+"/*")
		}
	}
	return shared.UniqueStrings(out)
}

func normalizePolicyMethods(methods []string) []string {
	if len(methods) == 0 {
		return nil
	}
	out := make([]string, 0, len(methods))
	for _, method := range methods {
		trimmed := strings.ToUpper(strings.TrimSpace(method))
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return shared.UniqueStrings(out)
}

func buildRuleHTTPMatches(paths []string, methods []string) []interface{} {
	if len(paths) == 0 && len(methods) == 0 {
		return nil
	}
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	matches := make([]interface{}, 0)
	for _, rawPath := range paths {
		path := normalizeRulePath(rawPath)
		if len(methods) == 0 {
			matches = append(matches, map[string]interface{}{
				"uri": map[string]interface{}{"prefix": path},
			})
			continue
		}
		for _, method := range methods {
			method = strings.TrimSpace(method)
			if method == "" {
				continue
			}
			matches = append(matches, map[string]interface{}{
				"uri":    map[string]interface{}{"prefix": path},
				"method": map[string]interface{}{"exact": strings.ToUpper(method)},
			})
		}
	}
	return matches
}

func normalizeRulePath(value string) string {
	path := strings.TrimSpace(value)
	if path == "" || path == "*" {
		return "/"
	}
	if strings.HasSuffix(path, "/*") {
		path = strings.TrimSuffix(path, "/*")
	}
	path = strings.TrimRight(path, "*")
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func resolveRuleHosts(hosts []string, gateways []string, serviceName, namespace string, cfg models.Config) []string {
	out := shared.UniqueStrings(hosts)
	if gatewayMatches(gateways, cfg.InternalGateway) && cfg.InternalDomain != "" {
		if !shared.HasHostSuffix(out, cfg.InternalDomain) {
			out = append(out, fmt.Sprintf("%s.%s", serviceName, cfg.InternalDomain))
		}
	}
	if gatewayMatches(gateways, cfg.ExternalGateway) && cfg.ExternalDomain != "" {
		if !shared.HasHostSuffix(out, cfg.ExternalDomain) {
			out = append(out, fmt.Sprintf("%s.%s", serviceName, cfg.ExternalDomain))
		}
	}
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace))
	}
	return shared.UniqueStrings(out)
}

func RuleVirtualServiceName(serviceName, ruleID string) string {
	base := shared.ToKubeName(ruleID)
	if base == "" {
		base = "rule"
	}
	if strings.HasPrefix(base, serviceName+"-") {
		return base
	}
	return fmt.Sprintf("%s-%s", serviceName, base)
}

func mapRuleGateways(gateways []string, cfg models.Config) []string {
	out := make([]string, 0, len(gateways))
	internalName := ""
	if cfg.InternalGateway != "" {
		internalName = pathBase(cfg.InternalGateway)
	}
	externalName := ""
	if cfg.ExternalGateway != "" {
		externalName = pathBase(cfg.ExternalGateway)
	}
	for _, gateway := range gateways {
		value := strings.TrimSpace(gateway)
		if value == "" {
			continue
		}
		if value == "mesh" {
			continue
		}
		if cfg.InternalGateway != "" && (value == cfg.InternalGateway || value == internalName) {
			out = append(out, cfg.InternalGateway)
			continue
		}
		if cfg.ExternalGateway != "" && (value == cfg.ExternalGateway || value == externalName) {
			out = append(out, cfg.ExternalGateway)
			continue
		}
		out = append(out, value)
	}
	return shared.UniqueStrings(out)
}

func pathBase(value string) string {
	if value == "" {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(value, "/"), "/")
	return parts[len(parts)-1]
}

func staticNginxHost(cfg models.Config) string {
	service := strings.TrimSpace(cfg.StaticNginxService)
	if service == "" {
		return ""
	}
	namespace := strings.TrimSpace(cfg.StaticNginxNamespace)
	if namespace == "" {
		return service
	}
	return fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace)
}

func gatewayMatches(gateways []string, target string) bool {
	if target == "" {
		return false
	}
	targetBase := pathBase(target)
	for _, gateway := range gateways {
		if gateway == target || gateway == targetBase {
			return true
		}
	}
	return false
}
