package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"releaseaworker/internal/platform/auth"
	httpheaders "releaseaworker/internal/platform/http/headers"
	"releaseaworker/internal/platform/models"
	"strconv"
	"strings"
)

var ErrOperationConflict = errors.New("operation conflict")

func FetchQueuedOperations(ctx context.Context, client *http.Client, cfg models.Config, tokens *auth.TokenManager) ([]models.OperationPayload, error) {
	endpoint := cfg.ApiBaseURL + "/operations?status=" + models.OperationStatusQueued + "&fairness=resource&limit=50"
	var operations []models.OperationPayload
	if err := DoJSONRequest(ctx, client, cfg, tokens, http.MethodGet, endpoint, nil, &operations, "operations fetch"); err != nil {
		return nil, err
	}
	return operations, nil
}

func FetchOperationsByStatus(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *auth.TokenManager,
	status string,
	opType string,
) ([]models.OperationPayload, error) {
	endpoint := cfg.ApiBaseURL + "/operations?status=" + status
	if opType != "" {
		endpoint += "&type=" + opType
	}

	var operations []models.OperationPayload
	if err := DoJSONRequest(ctx, client, cfg, tokens, http.MethodGet, endpoint, nil, &operations, "operations fetch"); err != nil {
		return nil, err
	}
	return operations, nil
}

func FetchOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *auth.TokenManager, opID string) (models.OperationPayload, error) {
	var op models.OperationPayload
	endpoint := cfg.ApiBaseURL + "/operations/" + opID
	if err := DoJSONRequest(ctx, client, cfg, tokens, http.MethodGet, endpoint, nil, &op, "operation fetch"); err != nil {
		return models.OperationPayload{}, err
	}
	return op, nil
}

func UpdateOperationStatus(ctx context.Context, client *http.Client, cfg models.Config, tokens *auth.TokenManager, opID, status, errMsg string) error {
	payload := map[string]string{"status": status}
	if errMsg != "" {
		payload["error"] = errMsg
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	token, err := tokens.Get(ctx, client, cfg)
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
	auth.SetAuthHeaders(req, token)
	httpheaders.SetContentTypeJSON(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		tokens.Invalidate()
		return fmt.Errorf("operation update unauthorized")
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrOperationConflict
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("operation update failed: %s", resp.Status)
	}
	return nil
}

func DoJSONRequest(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *auth.TokenManager,
	method string,
	url string,
	body []byte,
	target interface{},
	opLabel string,
) error {
	token, err := tokens.Get(ctx, client, cfg)
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
	auth.SetAuthHeaders(req, token)
	if len(body) > 0 {
		httpheaders.SetContentTypeJSON(req)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		tokens.Invalidate()
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

func AppendDeployLogs(ctx context.Context, client *http.Client, cfg models.Config, tokens *auth.TokenManager, deployID string, lines []string) error {
	if deployID == "" || len(lines) == 0 {
		return nil
	}
	endpoint := cfg.ApiBaseURL + "/deploys/" + deployID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, map[string]interface{}{"lines": lines}, "deploy logs update")
}

func AppendRuleLogs(ctx context.Context, client *http.Client, cfg models.Config, tokens *auth.TokenManager, ruleID string, lines []string) error {
	if ruleID == "" || len(lines) == 0 {
		return nil
	}
	endpoint := cfg.ApiBaseURL + "/rules/" + ruleID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, map[string]interface{}{"lines": lines}, "rule logs update")
}

func AppendRuleDeployLogs(ctx context.Context, client *http.Client, cfg models.Config, tokens *auth.TokenManager, ruleDeployID string, lines []string) error {
	if ruleDeployID == "" || len(lines) == 0 {
		return nil
	}
	endpoint := cfg.ApiBaseURL + "/rule-deploys/" + ruleDeployID + "/logs"
	return appendOperationPayload(ctx, client, cfg, tokens, endpoint, map[string]interface{}{"lines": lines}, "rule deploy logs update")
}

func UpdateDeployStrategyStatus(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *auth.TokenManager,
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
	cfg models.Config,
	tokens *auth.TokenManager,
	endpoint string,
	payload map[string]interface{},
	opLabel string,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return DoJSONRequest(ctx, client, cfg, tokens, http.MethodPost, endpoint, body, nil, opLabel)
}

func PayloadString(payload map[string]interface{}, key string) string {
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

func PayloadInt(payload map[string]interface{}, key string) int {
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

func PayloadResources(payload map[string]interface{}) ([]map[string]interface{}, error) {
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

func PayloadResourcesYAML(payload map[string]interface{}) string {
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

func UpdateBlueGreenActiveSlot(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *auth.TokenManager,
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
	return DoJSONRequest(ctx, client, cfg, tokens, http.MethodPost, endpoint, body, nil, "blue-green active slot update")
}
