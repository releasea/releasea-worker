package rules

import (
	"context"
	"net/http"

	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	"releaseaworker/internal/platform/models"
)

func reconcileEdgeResources(
	ctx context.Context,
	kubeClient *http.Client,
	kubeToken, appNamespace, serviceName string,
	rule models.RulePayload,
	service models.ServicePayload,
	cfg models.Config,
	gateways []string,
) error {
	gatewayNamespace := GatewayNamespace(cfg)
	hosts := resolveRuleHosts(rule.Hosts, gateways, serviceName, appNamespace, cfg)

	if policy := buildGatewayAuthorizationPolicy(rule, service, serviceName, gatewayNamespace, hosts); policy != nil {
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, policy); err != nil {
			return err
		}
	} else if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "AuthorizationPolicy", gatewayNamespace, GatewayEdgePolicyName(serviceName, rule.ID)); err != nil {
		return err
	}

	if RuleAction(rule) != "deny" && RuleAuthMode(rule) == "jwt" {
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, buildGatewayJWTRequestAuthentication(rule, service, serviceName, gatewayNamespace)); err != nil {
			return err
		}
	} else if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "RequestAuthentication", gatewayNamespace, GatewayJWTName(serviceName, rule.ID)); err != nil {
		return err
	}

	if filter := buildRouteEdgeFilter(rule, service, serviceName, gatewayNamespace); filter != nil {
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, buildGlobalEdgeFilter(gatewayNamespace)); err != nil {
			return err
		}
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, filter); err != nil {
			return err
		}
	} else if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "networking.istio.io/v1alpha3", "EnvoyFilter", gatewayNamespace, EdgeRateLimitName(serviceName, rule.ID)); err != nil {
		return err
	}

	if destination := buildEdgeDestinationRule(rule, service, serviceName, appNamespace); destination != nil {
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, destination); err != nil {
			return err
		}
	} else if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "networking.istio.io/v1beta1", "DestinationRule", appNamespace, EdgeDestinationRuleName(serviceName, rule.ID)); err != nil {
		return err
	}

	if peer := buildEdgePeerAuthentication(rule, service, serviceName, appNamespace); peer != nil {
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, peer); err != nil {
			return err
		}
	} else if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "PeerAuthentication", appNamespace, EdgePeerAuthenticationName(serviceName, rule.ID)); err != nil {
		return err
	}

	if networkPolicy := buildEdgeNetworkPolicy(rule, service, serviceName, appNamespace, gatewayNamespace); networkPolicy != nil {
		if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, networkPolicy); err != nil {
			return err
		}
	} else if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "networking.k8s.io/v1", "NetworkPolicy", appNamespace, EdgeNetworkPolicyName(serviceName, rule.ID)); err != nil {
		return err
	}
	return nil
}

func cleanupEdgeResources(ctx context.Context, kubeClient *http.Client, kubeToken, appNamespace, serviceName, ruleID string, cfg models.Config) error {
	gatewayNamespace := GatewayNamespace(cfg)
	deletes := []struct {
		apiVersion string
		kind       string
		namespace  string
		name       string
	}{
		{"security.istio.io/v1beta1", "AuthorizationPolicy", gatewayNamespace, GatewayEdgePolicyName(serviceName, ruleID)},
		{"security.istio.io/v1beta1", "RequestAuthentication", gatewayNamespace, GatewayJWTName(serviceName, ruleID)},
		{"networking.istio.io/v1alpha3", "EnvoyFilter", gatewayNamespace, EdgeRateLimitName(serviceName, ruleID)},
		{"networking.istio.io/v1beta1", "DestinationRule", appNamespace, EdgeDestinationRuleName(serviceName, ruleID)},
		{"security.istio.io/v1beta1", "PeerAuthentication", appNamespace, EdgePeerAuthenticationName(serviceName, ruleID)},
		{"networking.k8s.io/v1", "NetworkPolicy", appNamespace, EdgeNetworkPolicyName(serviceName, ruleID)},
	}
	for _, item := range deletes {
		if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, item.apiVersion, item.kind, item.namespace, item.name); err != nil {
			return err
		}
	}
	return nil
}
