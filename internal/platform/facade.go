package platform

import (
	"context"
	"net/http"

	platformauth "releaseaworker/internal/platform/auth"
	k8s "releaseaworker/internal/platform/integrations/kubernetes"
	ops "releaseaworker/internal/platform/integrations/operations"
	workerlog "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	workerqueue "releaseaworker/internal/platform/queue"
	workerutils "releaseaworker/internal/platform/utils"

	amqp "github.com/rabbitmq/amqp091-go"
)

type TokenManager = platformauth.TokenManager
type DeployLogger = workerlog.DeployLogger
type RuleDeployLogger = workerlog.RuleDeployLogger

var ErrOperationConflict = ops.ErrOperationConflict
var ErrDeploymentNotFound = k8s.ErrDeploymentNotFound

func NewTokenManager(token string) *TokenManager {
	return platformauth.NewTokenManager(token)
}

func SetAuthHeaders(req *http.Request, token string) {
	platformauth.SetAuthHeaders(req, token)
}

func DialRabbitMQ(rabbitURL string) (*amqp.Connection, error) {
	return workerqueue.DialRabbitMQ(rabbitURL)
}

func FetchQueuedOperations(ctx context.Context, client *http.Client, cfg models.Config, tokens *TokenManager) ([]models.OperationPayload, error) {
	return ops.FetchQueuedOperations(ctx, client, cfg, tokens)
}

func FetchOperationsByStatus(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *TokenManager,
	status string,
	opType string,
) ([]models.OperationPayload, error) {
	return ops.FetchOperationsByStatus(ctx, client, cfg, tokens, status, opType)
}

func FetchOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *TokenManager, opID string) (models.OperationPayload, error) {
	return ops.FetchOperation(ctx, client, cfg, tokens, opID)
}

func RecoverStaleOperationClaims(ctx context.Context, client *http.Client, cfg models.Config, tokens *TokenManager) (models.OperationClaimRecoveryResult, error) {
	return ops.RecoverStaleOperationClaims(ctx, client, cfg, tokens)
}

func ClaimOperation(ctx context.Context, client *http.Client, cfg models.Config, tokens *TokenManager, opID string) error {
	return ops.ClaimOperation(ctx, client, cfg, tokens, opID)
}

func UpdateOperationStatus(ctx context.Context, client *http.Client, cfg models.Config, tokens *TokenManager, opID, status, errMsg string) error {
	return ops.UpdateOperationStatus(ctx, client, cfg, tokens, opID, status, errMsg)
}

func DoJSONRequest(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *TokenManager,
	method string,
	url string,
	body []byte,
	target interface{},
	opLabel string,
) error {
	return ops.DoJSONRequest(ctx, client, cfg, tokens, method, url, body, target, opLabel)
}

func UpdateDeployStrategyStatus(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *TokenManager,
	deployID string,
	status string,
	strategyType string,
	phase string,
	summary string,
	details map[string]interface{},
) error {
	return ops.UpdateDeployStrategyStatus(ctx, client, cfg, tokens, deployID, status, strategyType, phase, summary, details)
}

func PayloadString(payload map[string]interface{}, key string) string {
	return ops.PayloadString(payload, key)
}

func PayloadInt(payload map[string]interface{}, key string) int {
	return ops.PayloadInt(payload, key)
}

func PayloadResources(payload map[string]interface{}) ([]map[string]interface{}, error) {
	return ops.PayloadResources(payload)
}

func PayloadResourcesYAML(payload map[string]interface{}) string {
	return ops.PayloadResourcesYAML(payload)
}

func UpdateBlueGreenActiveSlot(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *TokenManager,
	serviceID string,
	environment string,
	activeSlot string,
) error {
	return ops.UpdateBlueGreenActiveSlot(ctx, client, cfg, tokens, serviceID, environment, activeSlot)
}

func NewDeployLogger(client *http.Client, cfg models.Config, tokens *TokenManager, deployID string) *DeployLogger {
	return workerlog.NewDeployLogger(client, cfg, tokens, deployID)
}

func NewRuleDeployLogger(client *http.Client, cfg models.Config, tokens *TokenManager, ruleDeployID string) *RuleDeployLogger {
	return workerlog.NewRuleDeployLogger(client, cfg, tokens, ruleDeployID)
}

func KubeAPIBaseURL() string {
	return k8s.KubeAPIBaseURL()
}

func ApplyResource(ctx context.Context, client *http.Client, token string, resource map[string]interface{}) error {
	return k8s.ApplyResource(ctx, client, token, resource)
}

func DeleteResource(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) error {
	return k8s.DeleteResource(ctx, client, token, apiVersion, kind, namespace, name)
}

func DeleteResourcesByLabel(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, selector string) error {
	return k8s.DeleteResourcesByLabel(ctx, client, token, apiVersion, kind, namespace, selector)
}

func ResourceExists(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (bool, error) {
	return k8s.ResourceExists(ctx, client, token, apiVersion, kind, namespace, name)
}

func FetchResourceAsMap(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (map[string]interface{}, error) {
	return k8s.FetchResourceAsMap(ctx, client, token, apiVersion, kind, namespace, name)
}

func CleanResourceForReapply(resource map[string]interface{}) {
	k8s.CleanResourceForReapply(resource)
}

func ResourceURLs(apiVersion, kind, namespace, name string) (string, string, error) {
	return k8s.ResourceURLs(apiVersion, kind, namespace, name)
}

func KubeClient() (*http.Client, string, error) {
	return k8s.KubeClient()
}

func EnsureNamespace(ctx context.Context, client *http.Client, token, namespace string) error {
	return k8s.EnsureNamespace(ctx, client, token, namespace)
}

func RestartDeployment(ctx context.Context, cfg models.Config, payload map[string]interface{}) error {
	return k8s.RestartDeployment(ctx, cfg, payload)
}

func ScaleDeployment(ctx context.Context, deploymentNamespace, deploymentName string, replicas int) error {
	return k8s.ScaleDeployment(ctx, deploymentNamespace, deploymentName, replicas)
}

func GetDesiredAgents(ctx context.Context, cfg models.Config) int {
	return k8s.GetDesiredAgents(ctx, cfg)
}

func RunCommandWithLogger(ctx context.Context, workDir, name string, args []string, env []string, logger *DeployLogger) error {
	return workerutils.RunCommandWithLogger(ctx, workDir, name, args, env, logger)
}

func RunShellWithLogger(ctx context.Context, workDir, command string, logger *DeployLogger) error {
	return workerutils.RunShellWithLogger(ctx, workDir, command, logger)
}

func RunCommandOutput(ctx context.Context, workDir, name string, args []string, env []string) (string, error) {
	return workerutils.RunCommandOutput(ctx, workDir, name, args, env)
}

func RunCommandWithInput(ctx context.Context, name string, args []string, input string) (string, error) {
	return workerutils.RunCommandWithInput(ctx, name, args, input)
}

func DockerLogin(ctx context.Context, registry, username, password string) error {
	return workerutils.DockerLogin(ctx, registry, username, password)
}

func InjectToken(repoURL string, cred *models.SCMCredential) string {
	return workerutils.InjectToken(repoURL, cred)
}

func RegistryFromImage(image string) string {
	return workerutils.RegistryFromImage(image)
}

func NormalizeRegistryHost(value string) string {
	return workerutils.NormalizeRegistryHost(value)
}
