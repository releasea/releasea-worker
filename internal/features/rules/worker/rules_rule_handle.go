package rules

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	platformlog "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
)

func HandleRuleDeploy(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	if op.Resource == "" {
		return errors.New("rule id missing")
	}
	logger := platformlog.NewRuleDeployLogger(client, cfg, tokens, op.RuleDeployID)
	if logger != nil {
		defer logger.Flush(ctx)
		logger.Logf(ctx, "starting rule deploy operation=%s", op.ID)
	}
	rule, err := FetchRule(ctx, client, cfg, tokens, op.Resource)
	if err != nil {
		return err
	}
	if rule.ServiceID == "" {
		return errors.New("rule missing service id")
	}
	service, err := FetchService(ctx, client, cfg, tokens, rule.ServiceID)
	if err != nil {
		return err
	}
	if RuleAction(rule) == "deny" && strings.ToLower(strings.TrimSpace(rule.Protocol)) == "tcp" {
		return fmt.Errorf("deny rules are not supported for tcp protocol")
	}

	environment := strings.TrimSpace(rule.Environment)
	if environment == "" {
		environment = "prod"
	}
	if logger != nil {
		logger.Logf(ctx, "environment=%s service=%s rule=%s", environment, rule.ServiceID, rule.ID)
	}

	namespace := shared.ResolveNamespace(cfg, environment)
	if err := shared.ValidateAppNamespace(namespace); err != nil {
		return fmt.Errorf("rule deploy blocked: %w", err)
	}

	serviceName := shared.ToKubeName(service.Name)
	if serviceName == "" {
		serviceName = shared.ToKubeName(service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}

	gateways := mapRuleGateways(rule.Gateways, cfg)
	if RuleAction(rule) == "deny" {
		policyName := BuildDenyPolicyName(serviceName, rule.Name, rule.ID)
		kubeClient, kubeToken, err := platformkube.KubeClient()
		if err != nil {
			return err
		}
		if err := platformkube.EnsureNamespace(ctx, kubeClient, kubeToken, namespace); err != nil {
			return err
		}
		if len(gateways) == 0 {
			log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=delete authorizationpolicy=%s", rule.ID, environment, namespace, policyName)
			if logger != nil {
				logger.Logf(ctx, "delete authorization policy %s/%s", namespace, policyName)
			}
			return platformkube.DeleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, policyName)
		}
		policy := buildDenyAuthorizationPolicy(rule, service, serviceName, namespace, policyName)
		log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=apply authorizationpolicy=%s", rule.ID, environment, namespace, policyName)
		if logger != nil {
			logger.Logf(ctx, "apply authorization policy %s/%s", namespace, policyName)
		}
		return platformkube.ApplyResource(ctx, kubeClient, kubeToken, policy)
	}

	vsName := RuleVirtualServiceName(serviceName, rule.ID)
	if len(gateways) == 0 {
		log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=delete vs=%s", rule.ID, environment, namespace, vsName)
		if logger != nil {
			logger.Logf(ctx, "delete virtual service %s/%s", namespace, vsName)
		}
		kubeClient, kubeToken, err := platformkube.KubeClient()
		if err != nil {
			return err
		}
		if err := platformkube.DeleteResource(ctx, kubeClient, kubeToken, "networking.istio.io/v1beta1", "VirtualService", namespace, vsName); err != nil {
			return err
		}
		return nil
	}

	kubeClient, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return err
	}
	if err := platformkube.EnsureNamespace(ctx, kubeClient, kubeToken, namespace); err != nil {
		return err
	}
	service = normalizeRuleDeployStrategyForAvailability(ctx, kubeClient, kubeToken, namespace, serviceName, service, logger)
	service = applyRuleDeployStrategyOverride(service, op.Payload)
	vs := buildRuleVirtualService(rule, service, serviceName, namespace, cfg, gateways)
	log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=apply vs=%s gateways=%v", rule.ID, environment, namespace, vsName, gateways)
	if logger != nil {
		logger.Logf(ctx, "apply virtual service %s/%s gateways=%v", namespace, vsName, gateways)
	}
	if err := platformkube.ApplyResource(ctx, kubeClient, kubeToken, vs); err != nil {
		return err
	}
	strategyType := resolveDeployStrategyTypeForServicePayload(service)
	if cleanupErr := cleanupUnusedStrategyWorkloads(ctx, kubeClient, kubeToken, namespace, serviceName, strategyType, logger); cleanupErr != nil {
		log.Printf("[worker] rule.deploy cleanup skipped service=%s strategy=%s: %v", service.ID, strategyType, cleanupErr)
		if logger != nil {
			logger.Logf(ctx, "strategy cleanup skipped: %v", cleanupErr)
		}
	}
	return nil
}

func HandleRuleDelete(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	if op.Resource == "" {
		return errors.New("rule id missing")
	}

	logger := platformlog.NewRuleDeployLogger(client, cfg, tokens, op.RuleDeployID)
	if logger != nil {
		defer logger.Flush(ctx)
		logger.Logf(ctx, "starting rule delete operation=%s", op.ID)
	}

	environment := strings.TrimSpace(platformops.PayloadString(op.Payload, "environment"))
	if environment == "" {
		environment = "prod"
	}

	serviceName := strings.TrimSpace(platformops.PayloadString(op.Payload, "serviceName"))
	serviceID := strings.TrimSpace(platformops.PayloadString(op.Payload, "serviceId"))
	if serviceName == "" && serviceID != "" {
		service, err := FetchService(ctx, client, cfg, tokens, serviceID)
		if err == nil {
			serviceName = service.Name
		}
	}

	serviceName = shared.ToKubeName(serviceName)
	if serviceName == "" {
		serviceName = shared.ToKubeName(serviceID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}

	namespace := shared.ResolveNamespace(cfg, environment)
	if err := shared.ValidateAppNamespace(namespace); err != nil {
		return fmt.Errorf("rule delete blocked: %w", err)
	}

	action := strings.ToLower(strings.TrimSpace(platformops.PayloadString(op.Payload, "action")))
	if action == "deny" {
		ruleName := strings.TrimSpace(platformops.PayloadString(op.Payload, "ruleName"))
		policyName := BuildDenyPolicyName(serviceName, ruleName, op.Resource)
		log.Printf("[worker] rule.delete rule=%s env=%s namespace=%s action=delete authorizationpolicy=%s", op.Resource, environment, namespace, policyName)
		if logger != nil {
			logger.Logf(ctx, "delete authorization policy %s/%s", namespace, policyName)
		}
		kubeClient, kubeToken, err := platformkube.KubeClient()
		if err != nil {
			return err
		}
		return platformkube.DeleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, policyName)
	}

	vsName := RuleVirtualServiceName(serviceName, op.Resource)
	log.Printf("[worker] rule.delete rule=%s env=%s namespace=%s action=delete vs=%s", op.Resource, environment, namespace, vsName)
	if logger != nil {
		logger.Logf(ctx, "delete virtual service %s/%s", namespace, vsName)
	}

	kubeClient, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return err
	}
	return platformkube.DeleteResource(ctx, kubeClient, kubeToken, "networking.istio.io/v1beta1", "VirtualService", namespace, vsName)
}

func FetchRule(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, ruleID string) (models.RulePayload, error) {
	var rule models.RulePayload
	err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		cfg.ApiBaseURL+"/rules/"+ruleID,
		nil,
		&rule,
		"rule fetch",
	)
	return rule, err
}

func FetchService(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, serviceID string) (models.ServicePayload, error) {
	var service models.ServicePayload
	err := platformops.DoJSONRequest(
		ctx,
		client,
		cfg,
		tokens,
		http.MethodGet,
		cfg.ApiBaseURL+"/services/"+serviceID,
		nil,
		&service,
		"service fetch",
	)
	return service, err
}

func applyRuleDeployStrategyOverride(service models.ServicePayload, payload map[string]interface{}) models.ServicePayload {
	if payload == nil || !strings.EqualFold(strings.TrimSpace(service.DeploymentStrategy.Type), "canary") {
		return service
	}
	if _, ok := payload["canaryPercentOverride"]; !ok {
		return service
	}
	override := platformops.PayloadInt(payload, "canaryPercentOverride")
	if override < 0 {
		override = 0
	}
	if override > 50 {
		override = 50
	}
	service.DeploymentStrategy.CanaryPercent = override
	return service
}
