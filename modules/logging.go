package ops

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

type deployLogger struct {
	deployID string
	client   *http.Client
	cfg      Config
	tokens   *tokenManager
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

func newDeployLogger(client *http.Client, cfg Config, tokens *tokenManager, deployID string) *deployLogger {
	if deployID == "" {
		return nil
	}
	return &deployLogger{
		deployID: deployID,
		client:   client,
		cfg:      cfg,
		tokens:   tokens,
		maxBatch: 50,
	}
}

func (logger *deployLogger) Logf(ctx context.Context, format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.AppendLines(ctx, []string{fmt.Sprintf(format, args...)})
}

func (logger *deployLogger) AppendLines(ctx context.Context, lines []string) {
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

func (logger *deployLogger) Flush(ctx context.Context) {
	if logger == nil || len(logger.buffer) == 0 {
		return
	}
	if err := appendDeployLogs(ctx, logger.client, logger.cfg, logger.tokens, logger.deployID, logger.buffer); err != nil {
		log.Printf("[worker] deploy log flush failed: %v", err)
	}
	logger.buffer = nil
}

func (logger *deployLogger) UpdateStrategy(ctx context.Context, service serviceConfig, phase, summary string, extraDetails map[string]interface{}) {
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

type ruleLogger struct {
	ruleID   string
	client   *http.Client
	cfg      Config
	tokens   *tokenManager
	buffer   []string
	maxBatch int
}

func (logger *ruleLogger) Logf(ctx context.Context, format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.AppendLines(ctx, []string{fmt.Sprintf(format, args...)})
}

func (logger *ruleLogger) AppendLines(ctx context.Context, lines []string) {
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

func (logger *ruleLogger) Flush(ctx context.Context) {
	if logger == nil || len(logger.buffer) == 0 {
		return
	}
	if err := appendRuleLogs(ctx, logger.client, logger.cfg, logger.tokens, logger.ruleID, logger.buffer); err != nil {
		log.Printf("[worker] rule log flush failed: %v", err)
	}
	logger.buffer = nil
}

type ruleDeployLogger struct {
	ruleDeployID string
	client       *http.Client
	cfg          Config
	tokens       *tokenManager
	buffer       []string
	maxBatch     int
}

func newRuleDeployLogger(client *http.Client, cfg Config, tokens *tokenManager, ruleDeployID string) *ruleDeployLogger {
	if ruleDeployID == "" {
		return nil
	}
	return &ruleDeployLogger{
		ruleDeployID: ruleDeployID,
		client:       client,
		cfg:          cfg,
		tokens:       tokens,
		maxBatch:     50,
	}
}

func (logger *ruleDeployLogger) Logf(ctx context.Context, format string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.AppendLines(ctx, []string{fmt.Sprintf(format, args...)})
}

func (logger *ruleDeployLogger) AppendLines(ctx context.Context, lines []string) {
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

func (logger *ruleDeployLogger) Flush(ctx context.Context) {
	if logger == nil || len(logger.buffer) == 0 {
		return
	}
	if err := appendRuleDeployLogs(ctx, logger.client, logger.cfg, logger.tokens, logger.ruleDeployID, logger.buffer); err != nil {
		log.Printf("[worker] rule deploy log flush failed: %v", err)
	}
	logger.buffer = nil
}
