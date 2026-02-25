package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func fetchQueuedOperations(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager) ([]operationPayload, error) {
	return fetchOperationsByStatus(ctx, client, cfg, tokens, "queued", "")
}

func fetchOperationsByStatus(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	status string,
	opType string,
) ([]operationPayload, error) {
	endpoint := cfg.ApiBaseURL + "/operations?status=" + status
	if opType != "" {
		endpoint += "&type=" + opType
	}

	var operations []operationPayload
	if err := doJSONRequest(ctx, client, cfg, tokens, http.MethodGet, endpoint, nil, &operations, "operations fetch"); err != nil {
		return nil, err
	}
	return operations, nil
}

func fetchOperation(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, opID string) (operationPayload, error) {
	var op operationPayload
	endpoint := cfg.ApiBaseURL + "/operations/" + opID
	if err := doJSONRequest(ctx, client, cfg, tokens, http.MethodGet, endpoint, nil, &op, "operation fetch"); err != nil {
		return operationPayload{}, err
	}
	return op, nil
}

func updateOperationStatus(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, opID, status, errMsg string) error {
	payload := map[string]string{"status": status}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	token, err := tokens.get(ctx, client, cfg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		cfg.ApiBaseURL+"/operations/"+opID+"/status",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	setAuthHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		tokens.invalidate()
		return fmt.Errorf("operation update unauthorized")
	}
	if resp.StatusCode == http.StatusConflict {
		return errOperationConflict
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("operation update failed: %s", resp.Status)
	}
	return nil
}

func doJSONRequest(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	method string,
	url string,
	body []byte,
	target interface{},
	opLabel string,
) error {
	token, err := tokens.get(ctx, client, cfg)
	if err != nil {
		return err
	}

	var reader *bytes.Reader
	if len(body) == 0 {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	setAuthHeaders(req, token)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		tokens.invalidate()
		return fmt.Errorf("%s unauthorized", opLabel)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s failed: %s", opLabel, resp.Status)
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}
	return nil
}

func appendDeployLogs(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, deployID string, lines []string) error {
	if deployID == "" || len(lines) == 0 {
		return nil
	}
	endpoint := cfg.ApiBaseURL + "/deploys/" + deployID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, map[string]interface{}{"lines": lines}, "deploy logs update")
}

func appendRuleLogs(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, ruleID string, lines []string) error {
	if ruleID == "" || len(lines) == 0 {
		return nil
	}
	endpoint := cfg.ApiBaseURL + "/rules/" + ruleID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, map[string]interface{}{"lines": lines}, "rule logs update")
}

func appendRuleDeployLogs(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, ruleDeployID string, lines []string) error {
	if ruleDeployID == "" || len(lines) == 0 {
		return nil
	}
	endpoint := cfg.ApiBaseURL + "/rule-deploys/" + ruleDeployID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, map[string]interface{}{"lines": lines}, "rule deploy logs update")
}

func updateDeployStrategyStatus(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	deployID string,
	status string,
	strategyType string,
	phase string,
	summary string,
	details map[string]interface{},
) error {
	if strings.TrimSpace(deployID) == "" {
		return nil
	}
	statusPayload := map[string]interface{}{
		"type":    strings.TrimSpace(strategyType),
		"phase":   strings.TrimSpace(phase),
		"summary": strings.TrimSpace(summary),
	}
	if len(details) > 0 {
		statusPayload["details"] = details
	}
	payload := map[string]interface{}{
		"strategyStatus": statusPayload,
	}
	if strings.TrimSpace(status) != "" {
		payload["status"] = strings.TrimSpace(status)
	}
	endpoint := cfg.ApiBaseURL + "/deploys/" + deployID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, payload, "deploy strategy update")
}

func appendOperationPayload(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	endpoint string,
	payload map[string]interface{},
	opLabel string,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return doJSONRequest(ctx, client, cfg, tokens, http.MethodPost, endpoint, body, nil, opLabel)
}

func payloadString(payload map[string]interface{}, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key]; ok {
		if str, ok := value.(string); ok {
			return str
		}
	}
	return ""
}

func payloadInt(payload map[string]interface{}, key string) int {
	if payload == nil {
		return 0
	}
	if value, ok := payload[key]; ok {
		switch v := value.(type) {
		case int:
			return v
		case int32:
			return int(v)
		case int64:
			return int(v)
		case float32:
			return int(v)
		case float64:
			return int(v)
		case string:
			if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func payloadResources(payload map[string]interface{}) ([]map[string]interface{}, error) {
	if payload == nil {
		return nil, nil
	}
	raw, ok := payload["resources"]
	if !ok || raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case []interface{}:
		resources := make([]map[string]interface{}, 0, len(value))
		for _, item := range value {
			resource, ok := item.(map[string]interface{})
			if !ok {
				return nil, errors.New("invalid deploy resources payload")
			}
			resources = append(resources, resource)
		}
		return resources, nil
	case []map[string]interface{}:
		return value, nil
	default:
		return nil, errors.New("invalid deploy resources payload")
	}
}

func payloadResourcesYAML(payload map[string]interface{}) string {
	if payload == nil {
		return ""
	}
	value, ok := payload["resourcesYaml"]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

func updateBlueGreenActiveSlot(
	ctx context.Context,
	client *http.Client,
	cfg Config,
	tokens *tokenManager,
	serviceID string,
	environment string,
	activeSlot string,
) error {
	payload := map[string]string{
		"environment": environment,
		"activeSlot":  activeSlot,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/workers/services/%s/blue-green/primary", cfg.ApiBaseURL, serviceID)
	return doJSONRequest(ctx, client, cfg, tokens, http.MethodPost, endpoint, body, nil, "blue-green active slot update")
}
