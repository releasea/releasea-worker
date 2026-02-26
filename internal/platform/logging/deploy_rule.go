package logging

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"releaseaworker/internal/platform/auth"
	ops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	deploystrategy "releaseaworker/internal/platform/shared"
	"strings"
)

type DeployLogger struct {
	deployID string
	client   *http.Client
	cfg      models.Config
	tokens   *auth.TokenManager
	buffer   []string
	maxBatch int
}

type logSanitizerRule struct {
	pattern     *regexp.Regexp
	replacement string
}

var deployLogSanitizer = []logSanitizerRule{
	{pattern: regexp.MustCompile(`(?i)\bkubernetes\b`), replacement: "platform"},
	{pattern: regexp.MustCompile(`(?i)\bkubectl\b`), replacement: "platform tool"},
	{pattern: regexp.MustCompile(`(?i)\bmanifests?\b`), replacement: "configuration"},
	{pattern: regexp.MustCompile(`(?i)\bpods\b`), replacement: "instances"},
	{pattern: regexp.MustCompile(`(?i)\bpod\b`), replacement: "instance"},
	{pattern: regexp.MustCompile(`(?i)\breadiness\b`), replacement: "health"},
	{pattern: regexp.MustCompile(`(?i)\brollout\b`), replacement: "release"},
	{pattern: regexp.MustCompile(`(?i)\bapplying\b`), replacement: "preparing"},
	{pattern: regexp.MustCompile(`(?i)\bapplied\b`), replacement: "prepared"},
	{pattern: regexp.MustCompile(`(?i)\bapply\b`), replacement: "prepare"},
}

func sanitizeDeployLogLine(line string) string {
	out := line
	for _, rule := range deployLogSanitizer {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}

func NewDeployLogger(client *http.Client, cfg models.Config, tokens *auth.TokenManager, deployID string) *DeployLogger {
	if deployID == "" {
		return nil
	}
	return &DeployLogger{
		deployID: deployID,
		client:   client,
		cfg:      cfg,
		tokens:   tokens,
		maxBatch: 50,
	}
}

func (logger *DeployLogger) Logf(ctx context.Context, format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.AppendLines(ctx, []string{fmt.Sprintf(format, args...)})
}

func (logger *DeployLogger) AppendLines(ctx context.Context, lines []string) {
	if logger == nil || len(lines) == 0 {
		return
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		trimmed = sanitizeDeployLogLine(trimmed)
		logger.buffer = append(logger.buffer, trimmed)
		if len(logger.buffer) >= logger.maxBatch {
			logger.Flush(ctx)
		}
	}
}

func (logger *DeployLogger) Flush(ctx context.Context) {
	if logger == nil || len(logger.buffer) == 0 {
		return
	}
	if err := ops.AppendDeployLogs(ctx, logger.client, logger.cfg, logger.tokens, logger.deployID, logger.buffer); err != nil {
		log.Printf("[worker] deploy log flush failed: %v", err)
	}
	logger.buffer = nil
}

func (logger *DeployLogger) UpdateStrategy(ctx context.Context, service models.ServiceConfig, phase, summary string, extraDetails map[string]interface{}) {
	if logger == nil {
		return
	}
	if err := reportDeployStrategyProgress(
		ctx,
		logger.client,
		logger.cfg,
		logger.tokens,
		logger.deployID,
		service,
		phase,
		summary,
		extraDetails,
	); err != nil {
		log.Printf("[worker] deploy strategy status update failed: %v", err)
	}
}

type RuleDeployLogger struct {
	ruleDeployID string
	client       *http.Client
	cfg          models.Config
	tokens       *auth.TokenManager
	buffer       []string
	maxBatch     int
}

func NewRuleDeployLogger(client *http.Client, cfg models.Config, tokens *auth.TokenManager, ruleDeployID string) *RuleDeployLogger {
	if ruleDeployID == "" {
		return nil
	}
	return &RuleDeployLogger{
		ruleDeployID: ruleDeployID,
		client:       client,
		cfg:          cfg,
		tokens:       tokens,
		maxBatch:     50,
	}
}

func (logger *RuleDeployLogger) Logf(ctx context.Context, format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.AppendLines(ctx, []string{fmt.Sprintf(format, args...)})
}

func (logger *RuleDeployLogger) AppendLines(ctx context.Context, lines []string) {
	if logger == nil || len(lines) == 0 {
		return
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		logger.buffer = append(logger.buffer, trimmed)
		if len(logger.buffer) >= logger.maxBatch {
			logger.Flush(ctx)
		}
	}
}

func (logger *RuleDeployLogger) Flush(ctx context.Context) {
	if logger == nil || len(logger.buffer) == 0 {
		return
	}
	if err := ops.AppendRuleDeployLogs(ctx, logger.client, logger.cfg, logger.tokens, logger.ruleDeployID, logger.buffer); err != nil {
		log.Printf("[worker] rule deploy log flush failed: %v", err)
	}
	logger.buffer = nil
}

func reportDeployStrategyProgress(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *auth.TokenManager,
	deployID string,
	service models.ServiceConfig,
	status string,
	summary string,
	extraDetails map[string]interface{},
) error {
	if strings.TrimSpace(deployID) == "" {
		return nil
	}
	strategyType := resolveDeployStrategyType(service)
	details := buildDeployStrategyDetails(service)
	for key, value := range extraDetails {
		details[key] = value
	}
	return ops.UpdateDeployStrategyStatus(ctx, client, cfg, tokens, deployID, status, strategyType, status, summary, details)
}

func resolveDeployStrategyType(service models.ServiceConfig) string {
	return deploystrategy.NormalizeType(service.DeployTemplateID, service.DeploymentStrategy.Type)
}

func buildDeployStrategyDetails(service models.ServiceConfig) map[string]interface{} {
	details := map[string]interface{}{}
	switch resolveDeployStrategyType(service) {
	case "canary":
		canaryPercent := deploystrategy.NormalizeCanaryPercent(service.DeploymentStrategy.CanaryPercent)
		details["exposurePercent"] = canaryPercent
		details["stablePercent"] = 100 - canaryPercent
	case "blue-green":
		primary, secondary := resolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
		details["activeSlot"] = primary
		details["inactiveSlot"] = secondary
	default:
		targetReplicas := service.MinReplicas
		if targetReplicas < 1 {
			targetReplicas = service.Replicas
		}
		if targetReplicas < 1 {
			targetReplicas = 1
		}
		minReplicas := service.MinReplicas
		if minReplicas < 1 {
			minReplicas = 1
		}
		details["targetReplicas"] = targetReplicas
		details["minReplicas"] = minReplicas
		if service.MaxReplicas > 0 {
			details["maxReplicas"] = service.MaxReplicas
		}
	}
	return details
}

func resolveBlueGreenSlots(primary string) (string, string) {
	return deploystrategy.ResolveBlueGreenSlots(primary)
}
