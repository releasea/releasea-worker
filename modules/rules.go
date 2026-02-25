package ops

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
)

func handleRuleDeploy(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	if op.Resource == "" {
		return errors.New("rule id missing")
	}
	logger := newRuleDeployLogger(client, cfg, tokens, op.RuleDeployID)
	if logger != nil {
		defer logger.Flush(ctx)
		logger.Logf(ctx, "starting rule deploy operation=%s", op.ID)
	}
	rule, err := fetchRule(ctx, client, cfg, tokens, op.Resource)
	if err != nil {
		return err
	}
	if rule.ServiceID == "" {
		return errors.New("rule missing service id")
	}
	service, err := fetchService(ctx, client, cfg, tokens, rule.ServiceID)
	if err != nil {
		return err
	}
	if ruleAction(rule) == "deny" && strings.ToLower(strings.TrimSpace(rule.Protocol)) == "tcp" {
		return fmt.Errorf("deny rules are not supported for tcp protocol")
	}

	environment := strings.TrimSpace(rule.Environment)
	if environment == "" {
		environment = "prod"
	}
	if logger != nil {
		logger.Logf(ctx, "environment=%s service=%s rule=%s", environment, rule.ServiceID, rule.ID)
	}

	namespace := resolveNamespace(cfg, environment)
	if err := validateAppNamespace(namespace); err != nil {
		return fmt.Errorf("rule deploy blocked: %w", err)
	}

	serviceName := toKubeName(service.Name)
	if serviceName == "" {
		serviceName = toKubeName(service.ID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}

	gateways := mapRuleGateways(rule.Gateways, cfg)
	if ruleAction(rule) == "deny" {
		policyName := buildDenyPolicyName(serviceName, rule.Name, rule.ID)
		kubeClient, kubeToken, err := kubeClient()
		if err != nil {
			return err
		}
		if err := ensureNamespace(ctx, kubeClient, kubeToken, namespace); err != nil {
			return err
		}
		if len(gateways) == 0 {
			log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=delete authorizationpolicy=%s", rule.ID, environment, namespace, policyName)
			if logger != nil {
				logger.Logf(ctx, "delete authorization policy %s/%s", namespace, policyName)
			}
			return deleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, policyName)
		}
		policy := buildDenyAuthorizationPolicy(rule, service, serviceName, namespace, policyName)
		log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=apply authorizationpolicy=%s", rule.ID, environment, namespace, policyName)
		if logger != nil {
			logger.Logf(ctx, "apply authorization policy %s/%s", namespace, policyName)
		}
		return applyResource(ctx, kubeClient, kubeToken, policy)
	}

	vsName := ruleVirtualServiceName(serviceName, rule.ID)
	if len(gateways) == 0 {
		log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=delete vs=%s", rule.ID, environment, namespace, vsName)
		if logger != nil {
			logger.Logf(ctx, "delete virtual service %s/%s", namespace, vsName)
		}
		kubeClient, kubeToken, err := kubeClient()
		if err != nil {
			return err
		}
		if err := deleteResource(ctx, kubeClient, kubeToken, "networking.istio.io/v1beta1", "VirtualService", namespace, vsName); err != nil {
			return err
		}
		return nil
	}

	kubeClient, kubeToken, err := kubeClient()
	if err != nil {
		return err
	}
	if err := ensureNamespace(ctx, kubeClient, kubeToken, namespace); err != nil {
		return err
	}
	service = normalizeRuleDeployStrategyForAvailability(ctx, kubeClient, kubeToken, namespace, serviceName, service, logger)
	service = applyRuleDeployStrategyOverride(service, op.Payload)
	vs := buildRuleVirtualService(rule, service, serviceName, namespace, cfg, gateways)
	log.Printf("[worker] rule.deploy rule=%s env=%s namespace=%s action=apply vs=%s gateways=%v", rule.ID, environment, namespace, vsName, gateways)
	if logger != nil {
		logger.Logf(ctx, "apply virtual service %s/%s gateways=%v", namespace, vsName, gateways)
	}
	if err := applyResource(ctx, kubeClient, kubeToken, vs); err != nil {
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

func handleRuleDelete(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, op operationPayload) error {
	if op.Resource == "" {
		return errors.New("rule id missing")
	}

	logger := newRuleDeployLogger(client, cfg, tokens, op.RuleDeployID)
	if logger != nil {
		defer logger.Flush(ctx)
		logger.Logf(ctx, "starting rule delete operation=%s", op.ID)
	}

	environment := strings.TrimSpace(payloadString(op.Payload, "environment"))
	if environment == "" {
		environment = "prod"
	}

	serviceName := strings.TrimSpace(payloadString(op.Payload, "serviceName"))
	serviceID := strings.TrimSpace(payloadString(op.Payload, "serviceId"))
	if serviceName == "" && serviceID != "" {
		service, err := fetchService(ctx, client, cfg, tokens, serviceID)
		if err == nil {
			serviceName = service.Name
		}
	}

	serviceName = toKubeName(serviceName)
	if serviceName == "" {
		serviceName = toKubeName(serviceID)
	}
	if serviceName == "" {
		return errors.New("service name invalid")
	}

	namespace := resolveNamespace(cfg, environment)
	if err := validateAppNamespace(namespace); err != nil {
		return fmt.Errorf("rule delete blocked: %w", err)
	}

	action := strings.ToLower(strings.TrimSpace(payloadString(op.Payload, "action")))
	if action == "deny" {
		ruleName := strings.TrimSpace(payloadString(op.Payload, "ruleName"))
		policyName := buildDenyPolicyName(serviceName, ruleName, op.Resource)
		log.Printf("[worker] rule.delete rule=%s env=%s namespace=%s action=delete authorizationpolicy=%s", op.Resource, environment, namespace, policyName)
		if logger != nil {
			logger.Logf(ctx, "delete authorization policy %s/%s", namespace, policyName)
		}
		kubeClient, kubeToken, err := kubeClient()
		if err != nil {
			return err
		}
		return deleteResource(ctx, kubeClient, kubeToken, "security.istio.io/v1beta1", "AuthorizationPolicy", namespace, policyName)
	}

	vsName := ruleVirtualServiceName(serviceName, op.Resource)
	log.Printf("[worker] rule.delete rule=%s env=%s namespace=%s action=delete vs=%s", op.Resource, environment, namespace, vsName)
	if logger != nil {
		logger.Logf(ctx, "delete virtual service %s/%s", namespace, vsName)
	}

	kubeClient, kubeToken, err := kubeClient()
	if err != nil {
		return err
	}
	return deleteResource(ctx, kubeClient, kubeToken, "networking.istio.io/v1beta1", "VirtualService", namespace, vsName)
}

func fetchRule(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, ruleID string) (rulePayload, error) {
	var rule rulePayload
	err := doJSONRequest(
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

func fetchService(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, serviceID string) (servicePayload, error) {
	var service servicePayload
	err := doJSONRequest(
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

func applyRuleDeployStrategyOverride(service servicePayload, payload map[string]interface{}) servicePayload {
	if payload == nil || !strings.EqualFold(strings.TrimSpace(service.DeploymentStrategy.Type), "canary") {
		return service
	}
	if _, ok := payload["canaryPercentOverride"]; !ok {
		return service
	}
	override := payloadInt(payload, "canaryPercentOverride")
	if override < 0 {
		override = 0
	}
	if override > 50 {
		override = 50
	}
	service.DeploymentStrategy.CanaryPercent = override
	return service
}
