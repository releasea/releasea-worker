package operations

import deployrule "releaseaworker/operations/deploy_rule"

func buildRuleVirtualService(rule rulePayload, service servicePayload, serviceName, namespace string, cfg Config, gateways []string) map[string]interface{} {
	return deployrule.BuildVirtualService(rule, service, serviceName, namespace, cfg, gateways)
}

func ruleAction(rule rulePayload) string {
	return deployrule.RuleAction(rule)
}

func buildDenyPolicyName(serviceName, ruleName, ruleID string) string {
	return deployrule.BuildDenyPolicyName(serviceName, ruleName, ruleID)
}

func buildDenyAuthorizationPolicy(rule rulePayload, service servicePayload, serviceName, namespace, policyName string) map[string]interface{} {
	return deployrule.BuildDenyAuthorizationPolicy(rule, service, serviceName, namespace, policyName)
}

func ruleVirtualServiceName(serviceName, ruleID string) string {
	return deployrule.RuleVirtualServiceName(serviceName, ruleID)
}

func mapRuleGateways(gateways []string, cfg Config) []string {
	return deployrule.MapGateways(gateways, cfg)
}
