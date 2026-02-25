package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	platformauth "releaseaworker/internal/platform/auth"
	platformkube "releaseaworker/internal/platform/integrations/kubernetes"
	platformops "releaseaworker/internal/platform/integrations/operations"
	platformlogging "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	platformutils "releaseaworker/internal/platform/utils"
	"strings"
)

func HandleServiceDeploy(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, op models.OperationPayload) error {
	if op.Resource == "" {
		return errors.New("service id missing")
	}

	logger := platformlogging.NewDeployLogger(client, cfg, tokens, op.DeployID)
	if logger != nil {
		defer logger.Flush(ctx)
	}

	environment := resolveOperationEnvironment(op.Payload)
	if logger != nil {
		logger.Logf(ctx, "starting deploy for service=%s env=%s", op.Resource, environment)
	}

	contextData, err := fetchServiceContext(ctx, client, cfg, tokens, op.Resource, environment)
	if err != nil {
		return err
	}
	if deployImage := strings.TrimSpace(platformops.PayloadString(op.Payload, "image")); deployImage != "" {
		contextData.Service.DockerImage = deployImage
	}

	reportStrategy := createStrategyReporter(ctx, logger, &contextData.Service)
	reportStrategy(deployStatusPreparing, "Preparing new version", map[string]interface{}{"environment": environment})

	staticSiteHandled, err := deployStaticSiteIfNeeded(ctx, cfg, contextData, environment, logger, reportStrategy)
	if err != nil {
		return err
	}
	if staticSiteHandled {
		return nil
	}

	if err := prepareServiceArtifact(ctx, client, cfg, tokens, op.Payload, environment, &contextData, logger, reportStrategy); err != nil {
		return err
	}

	strategyType, serviceName, err := applyServiceResources(ctx, cfg, op.Payload, environment, contextData, logger, reportStrategy)
	if err != nil {
		return err
	}
	if logger != nil {
		logger.Logf(ctx, "deploy completed")
	}

	if err := validateDeploymentReadiness(ctx, cfg, environment, op.Payload, strategyType, serviceName, contextData.Service, logger, reportStrategy); err != nil {
		return err
	}

	if err := promoteDeployStrategy(ctx, client, cfg, tokens, op, environment, strategyType, serviceName, &contextData, logger, reportStrategy); err != nil {
		return err
	}

	reportStrategy(deployStatusCompleted, "Version active for user traffic", nil)
	return nil
}

type strategyReporter func(phase, summary string, details map[string]interface{})

func createStrategyReporter(ctx context.Context, logger *platformlogging.DeployLogger, service *models.ServiceConfig) strategyReporter {
	return func(phase, summary string, details map[string]interface{}) {
		if logger == nil || service == nil {
			return
		}
		logger.UpdateStrategy(ctx, *service, phase, summary, details)
	}
}

func resolveOperationEnvironment(payload map[string]interface{}) string {
	environment := platformops.PayloadString(payload, "environment")
	if environment == "" {
		return "prod"
	}
	return environment
}

func resolveDeploySourceType(service models.ServiceConfig) string {
	sourceType := shared.NormalizeSourceType(service.SourceType)
	if sourceType != "" {
		return sourceType
	}
	if service.RepoURL != "" {
		return "git"
	}
	if service.DockerImage != "" {
		return "registry"
	}
	return ""
}

func resolveTargetCommit(payload map[string]interface{}) string {
	targetCommit := strings.TrimSpace(platformops.PayloadString(payload, "version"))
	if targetCommit != "" {
		return targetCommit
	}
	return strings.TrimSpace(platformops.PayloadString(payload, "commitSha"))
}

func resolveServiceName(service models.ServiceConfig) string {
	serviceName := shared.ToKubeName(service.Name)
	if serviceName != "" {
		return serviceName
	}
	return shared.ToKubeName(service.ID)
}

func deployStaticSiteIfNeeded(
	ctx context.Context,
	cfg models.Config,
	contextData models.DeployContext,
	environment string,
	logger *platformlogging.DeployLogger,
	reportStrategy strategyReporter,
) (bool, error) {
	if strings.ToLower(contextData.Service.Type) != "static-site" {
		return false, nil
	}
	reportStrategy(deployStatusDeploying, "Publishing static site content", nil)
	if logger != nil {
		logger.Logf(ctx, "detected static site deploy, skipping image build")
	}
	if err := handleStaticSiteDeploy(ctx, cfg, contextData, environment, logger); err != nil {
		return true, err
	}
	if logger != nil {
		logger.Logf(ctx, "static site deploy completed")
	}
	reportStrategy(deployStatusValidating, "Validating application health", map[string]interface{}{"mode": "static-site"})
	reportStrategy(deployStatusCompleted, "Version active for user traffic", nil)
	return true, nil
}

func prepareServiceArtifact(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	payload map[string]interface{},
	environment string,
	contextData *models.DeployContext,
	logger *platformlogging.DeployLogger,
	reportStrategy strategyReporter,
) error {
	if contextData == nil {
		return errors.New("deploy context missing")
	}

	sourceType := resolveDeploySourceType(contextData.Service)
	switch sourceType {
	case "git":
		if contextData.Service.RepoURL == "" {
			return errors.New("repository URL not set")
		}
		if contextData.Service.DockerImage == "" {
			return errors.New("target docker image not set")
		}
		reportStrategy(deployStatusPreparing, "Preparing application package", nil)
		if err := buildAndPushImageFull(
			ctx,
			cfg,
			contextData,
			client,
			tokens,
			environment,
			resolveTargetCommit(payload),
			logger,
		); err != nil {
			return err
		}
	case "registry":
		if contextData.Service.DockerImage == "" {
			return errors.New("docker image not set")
		}
		reportStrategy(deployStatusPreparing, "Using existing application package", map[string]interface{}{"image": contextData.Service.DockerImage})
		if logger != nil {
			logger.Logf(ctx, "using registry image %s", contextData.Service.DockerImage)
		}
	default:
		return errors.New("unknown source type")
	}
	return nil
}

func applyServiceResources(
	ctx context.Context,
	cfg models.Config,
	payload map[string]interface{},
	environment string,
	contextData models.DeployContext,
	logger *platformlogging.DeployLogger,
	reportStrategy strategyReporter,
) (string, string, error) {
	reportStrategy(deployStatusDeploying, "Deploying version to environment", nil)
	if logger != nil {
		logger.Logf(ctx, "publishing version resources")
	}

	strategyType := ResolveDeployStrategyType(contextData.Service)
	serviceName := resolveServiceName(contextData.Service)
	resources, resourcesErr := platformops.PayloadResources(payload)
	if resourcesErr != nil {
		return "", "", resourcesErr
	}

	// Prefer structured resources over raw YAML so we can inject replicas, resources, and strategy policy.
	if len(resources) > 0 {
		if err := applyRenderedResources(ctx, cfg, resources, environment, contextData, logger); err != nil {
			return "", "", err
		}
		if err := reconcileStrategyResources(ctx, cfg, contextData, environment, serviceName, resources, logger); err != nil {
			return "", "", err
		}
		return strategyType, serviceName, nil
	}

	resourcesYAML := platformops.PayloadResourcesYAML(payload)
	if strings.TrimSpace(resourcesYAML) != "" {
		if err := applyResourcesYAML(ctx, resourcesYAML, logger); err != nil {
			return "", "", err
		}
		if strategyType != "canary" && strategyType != "blue-green" {
			stampDeployRevisionKubectl(ctx, cfg, environment, serviceName, contextData.Service, logger)
		}
		if err := reconcileStrategyResources(ctx, cfg, contextData, environment, serviceName, nil, logger); err != nil {
			return "", "", err
		}
		return strategyType, serviceName, nil
	}

	if err := applyDeployResources(ctx, cfg, contextData, environment, logger); err != nil {
		return "", "", err
	}
	return strategyType, serviceName, nil
}

func validateDeploymentReadiness(
	ctx context.Context,
	cfg models.Config,
	environment string,
	payload map[string]interface{},
	strategyType string,
	serviceName string,
	service models.ServiceConfig,
	logger *platformlogging.DeployLogger,
	reportStrategy strategyReporter,
) error {
	reportStrategy(deployStatusValidating, "Validating application health", nil)

	namespace := ResolveDeployNamespaceFromPayload(payload, shared.ResolveNamespace(cfg, environment))
	targets := resolveDeployReadinessTargets(service, serviceName, payload)
	if strategyType == "canary" {
		targets = resolveCanaryReadinessTargets(ctx, cfg, namespace, serviceName, targets, logger)
	}
	if strategyType == "blue-green" && serviceName != "" {
		_, candidateSlot := ResolveBlueGreenSlots(service.DeploymentStrategy.BlueGreenPrimary)
		if candidateSlot != "" {
			targets = []string{serviceName + "-" + candidateSlot}
		}
	}

	return waitForServiceDeployReadiness(
		ctx,
		cfg,
		environment,
		namespace,
		serviceName,
		targets,
		service,
		logger,
	)
}

func resolveCanaryExposurePercent(service models.ServiceConfig) int {
	exposurePercent := service.DeploymentStrategy.CanaryPercent
	if exposurePercent <= 0 {
		exposurePercent = 10
	}
	if exposurePercent > 50 {
		exposurePercent = 50
	}
	return exposurePercent
}

func promoteDeployStrategy(
	ctx context.Context,
	client *http.Client,
	cfg models.Config,
	tokens *platformauth.TokenManager,
	op models.OperationPayload,
	environment string,
	strategyType string,
	serviceName string,
	contextData *models.DeployContext,
	logger *platformlogging.DeployLogger,
	reportStrategy strategyReporter,
) error {
	if contextData == nil {
		return errors.New("deploy context missing")
	}

	switch strategyType {
	case "rolling":
		if err := promoteRollingTraffic(ctx, cfg, environment, serviceName, logger); err != nil {
			return err
		}
		cleanupStrategyShadowsBestEffort(ctx, cfg, environment, serviceName, "rolling", logger)
	case "canary":
		reportStrategy(deployStatusProgressing, "Gradually increasing traffic exposure", map[string]interface{}{
			"exposurePercent": resolveCanaryExposurePercent(contextData.Service),
		})
		reportStrategy(deployStatusPromoting, "Promoting version to active traffic", nil)
	case "blue-green":
		activeSlot, candidateSlot := ResolveBlueGreenSlots(contextData.Service.DeploymentStrategy.BlueGreenPrimary)
		reportStrategy(deployStatusPromoting, "Switching active environment", map[string]interface{}{
			"activeSlot":    activeSlot,
			"candidateSlot": candidateSlot,
		})
		promotedSlot, err := promoteBlueGreen(
			ctx,
			client,
			cfg,
			tokens,
			contextData.Service,
			op.Resource,
			environment,
			serviceName,
			logger,
		)
		if err != nil {
			return err
		}
		contextData.Service.DeploymentStrategy.BlueGreenPrimary = promotedSlot
	}
	return nil
}

func resolveCanaryReadinessTargets(
	ctx context.Context,
	cfg models.Config,
	namespace string,
	serviceName string,
	targets []string,
	logger *platformlogging.DeployLogger,
) []string {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" || len(targets) == 0 {
		return targets
	}

	hasStableTarget := false
	for _, target := range targets {
		if strings.TrimSpace(target) == serviceName {
			hasStableTarget = true
			break
		}
	}
	if !hasStableTarget {
		return targets
	}

	kubeHTTP, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		return targets
	}

	stableExists, err := platformkube.ResourceExists(ctx, kubeHTTP, kubeToken, "apps/v1", "Deployment", namespace, serviceName)
	if err != nil || stableExists {
		return targets
	}

	canonicalService, err := platformkube.FetchResourceAsMap(ctx, kubeHTTP, kubeToken, "v1", "Service", namespace, serviceName)
	if err != nil {
		return targets
	}
	spec := shared.MapValue(canonicalService["spec"])
	selector := shared.MapValue(spec["selector"])
	canonicalTarget := strings.TrimSpace(shared.StringValue(selector, "app"))
	if canonicalTarget == "" || canonicalTarget == serviceName {
		return targets
	}

	canonicalDeploymentExists, err := platformkube.ResourceExists(ctx, kubeHTTP, kubeToken, "apps/v1", "Deployment", namespace, canonicalTarget)
	if err != nil || !canonicalDeploymentExists {
		return targets
	}

	nextTargets := make([]string, 0, len(targets))
	for _, target := range targets {
		if strings.TrimSpace(target) == serviceName {
			nextTargets = append(nextTargets, canonicalTarget)
			continue
		}
		nextTargets = append(nextTargets, target)
	}
	nextTargets = shared.UniqueStrings(nextTargets)

	if logger != nil {
		logger.Logf(ctx, "canary readiness: using %s as stable target while canonical traffic remains on it", canonicalTarget)
		logger.Flush(ctx)
	}

	return nextTargets
}

func cleanupStrategyShadowsBestEffort(
	ctx context.Context,
	cfg models.Config,
	environment string,
	serviceName string,
	strategyType string,
	logger *platformlogging.DeployLogger,
) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return
	}
	namespace := shared.ResolveNamespace(cfg, environment)
	kubeHTTP, kubeToken, err := platformkube.KubeClient()
	if err != nil {
		log.Printf("[worker] strategy shadow cleanup skipped service=%s: %v", serviceName, err)
		if logger != nil {
			logger.Logf(ctx, "strategy shadow cleanup skipped: %v", err)
			logger.Flush(ctx)
		}
		return
	}
	if err := cleanupUnusedStrategyWorkloads(ctx, kubeHTTP, kubeToken, namespace, serviceName, strategyType, logger); err != nil {
		log.Printf("[worker] strategy shadow cleanup skipped service=%s: %v", serviceName, err)
		if logger != nil {
			logger.Logf(ctx, "strategy shadow cleanup skipped: %v", err)
			logger.Flush(ctx)
		}
	}
}

type strategyCleanupLogger interface {
	Logf(ctx context.Context, format string, args ...interface{})
	Flush(ctx context.Context)
}

func cleanupUnusedStrategyWorkloads(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
	strategyType string,
	logger strategyCleanupLogger,
) error {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil
	}

	candidates := cleanupCandidatesForStrategy(serviceName, strategyType)
	if len(candidates) == 0 {
		return nil
	}

	hostsInUse, err := listVirtualServiceDestinationHosts(ctx, client, token, namespace)
	if err != nil {
		return err
	}
	canonicalTarget, err := canonicalServiceTarget(ctx, client, token, namespace, serviceName)
	if err != nil {
		return err
	}

	for _, candidate := range candidates {
		if candidate == canonicalTarget {
			if logger != nil {
				logger.Logf(ctx, "keeping %s while canonical service still targets it", candidate)
			}
			continue
		}
		if isWorkloadAliasReferenced(hostsInUse, candidate, namespace) {
			if logger != nil {
				logger.Logf(ctx, "keeping %s while traffic still references it", candidate)
			}
			continue
		}
		if err := platformkube.DeleteResource(ctx, client, token, "apps/v1", "Deployment", namespace, candidate); err != nil {
			return err
		}
		if err := platformkube.DeleteResource(ctx, client, token, "v1", "Service", namespace, candidate); err != nil {
			return err
		}
		if logger != nil {
			logger.Logf(ctx, "cleanup shadow workload %s", candidate)
		}
	}
	if logger != nil {
		logger.Flush(ctx)
	}
	return nil
}

func cleanupCandidatesForStrategy(serviceName string, strategyType string) []string {
	switch strings.ToLower(strings.TrimSpace(strategyType)) {
	case "rolling":
		return []string{serviceName + "-canary", serviceName + "-blue", serviceName + "-green"}
	case "canary":
		return []string{serviceName + "-blue", serviceName + "-green"}
	case "blue-green":
		return []string{serviceName + "-canary"}
	default:
		return nil
	}
}

func listVirtualServiceDestinationHosts(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
) (map[string]struct{}, error) {
	_, listURL, err := platformkube.ResourceURLs("networking.istio.io/v1beta1", "VirtualService", namespace, "placeholder")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return map[string]struct{}{}, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("virtual service list failed: %s", resp.Status)
	}

	var payload struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	hosts := make(map[string]struct{})
	for _, vs := range payload.Items {
		collectDestinationHosts(vs, hosts)
	}
	return hosts, nil
}

func collectDestinationHosts(value interface{}, out map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, nested := range typed {
			if strings.EqualFold(strings.TrimSpace(key), "host") {
				if host, ok := nested.(string); ok {
					normalized := strings.ToLower(strings.TrimSpace(host))
					if normalized != "" {
						out[normalized] = struct{}{}
					}
				}
				continue
			}
			collectDestinationHosts(nested, out)
		}
	case []interface{}:
		for _, item := range typed {
			collectDestinationHosts(item, out)
		}
	}
}

func isWorkloadAliasReferenced(hosts map[string]struct{}, alias string, namespace string) bool {
	for host := range hosts {
		if hostMatchesWorkloadAlias(host, alias, namespace) {
			return true
		}
	}
	return false
}

func canonicalServiceTarget(
	ctx context.Context,
	client *http.Client,
	token string,
	namespace string,
	serviceName string,
) (string, error) {
	exists, err := platformkube.ResourceExists(ctx, client, token, "v1", "Service", namespace, serviceName)
	if err != nil || !exists {
		return "", err
	}
	canonicalService, err := platformkube.FetchResourceAsMap(ctx, client, token, "v1", "Service", namespace, serviceName)
	if err != nil {
		return "", err
	}
	spec := shared.MapValue(canonicalService["spec"])
	selector := shared.MapValue(spec["selector"])
	return strings.TrimSpace(shared.StringValue(selector, "app")), nil
}

func hostMatchesWorkloadAlias(host string, alias string, namespace string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	alias = strings.ToLower(strings.TrimSpace(alias))
	namespace = strings.ToLower(strings.TrimSpace(namespace))
	if host == "" || alias == "" {
		return false
	}
	candidates := []string{
		alias,
		alias + "." + namespace,
		alias + "." + namespace + ".svc",
		alias + "." + namespace + ".svc.cluster.local",
	}
	for _, candidate := range candidates {
		if host == candidate {
			return true
		}
	}
	return false
}

func buildAndPushImageFull(
	ctx context.Context,
	cfg models.Config,
	ctxData *models.DeployContext,
	client *http.Client,
	tokens *platformauth.TokenManager,
	environment string,
	targetCommit string,
	logger *platformlogging.DeployLogger,
) error {
	if ctxData == nil {
		return errors.New("deploy context missing")
	}
	workspace, err := os.MkdirTemp("", "releasea-worker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)

	repoURL := ctxData.Service.RepoURL
	if ctxData.SCM != nil && ctxData.SCM.Token != "" {
		repoURL = platformutils.InjectToken(repoURL, ctxData.SCM)
	}

	cloneArgs := []string{"clone", "--depth", "1"}
	if ctxData.Service.Branch != "" {
		cloneArgs = append(cloneArgs, "--branch", ctxData.Service.Branch)
	}
	cloneArgs = append(cloneArgs, repoURL, workspace)

	log.Printf("[worker] cloning repository for service %s", ctxData.Service.Name)
	if logger != nil {
		logger.Logf(ctx, "cloning repository %s", ctxData.Service.RepoURL)
	}
	if err := platformutils.RunCommandWithLogger(ctx, workspace, "git", cloneArgs, nil, logger); err != nil {
		return err
	}
	if logger != nil {
		logger.Flush(ctx)
	}

	normalizedTargetCommit := normalizeTargetCommitRef(targetCommit)
	if strings.TrimSpace(targetCommit) != "" && normalizedTargetCommit == "" && logger != nil {
		logger.Logf(ctx, "ignoring invalid target commit reference %q and using branch head", targetCommit)
	}

	if normalizedTargetCommit != "" {
		if logger != nil {
			logger.Logf(ctx, "checking out commit %s", normalizedTargetCommit)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workspace, "git", []string{"fetch", "--depth", "1", "origin", normalizedTargetCommit}, nil, logger); err != nil {
			return fmt.Errorf("fetch target commit: %w", err)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workspace, "git", []string{"checkout", normalizedTargetCommit}, nil, logger); err != nil {
			return fmt.Errorf("checkout target commit: %w", err)
		}
	}

	commitSHA, err := platformutils.RunCommandOutput(ctx, workspace, "git", []string{"rev-parse", "HEAD"}, nil)
	if err != nil {
		log.Printf("[worker] failed to resolve git commit: %v", err)
		commitSHA = ""
	}
	shortSHA := ""
	if commitSHA != "" {
		if len(commitSHA) > 8 {
			shortSHA = commitSHA[:8]
		} else {
			shortSHA = commitSHA
		}
	}

	workDir := workspace
	if ctxData.Service.RootDir != "" {
		workDir = filepath.Join(workspace, ctxData.Service.RootDir)
	}

	if ctxData.Service.PreDeployCommand != "" {
		log.Printf("[worker] running pre-deploy command for %s", ctxData.Service.Name)
		if logger != nil {
			logger.Logf(ctx, "running pre-deploy command")
		}
		if err := platformutils.RunShellWithLogger(ctx, workDir, ctxData.Service.PreDeployCommand, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	image := ctxData.Service.DockerImage
	if image == "" {
		return errors.New("docker image missing")
	}

	baseImage := image
	lastColon := strings.LastIndex(image, ":")
	lastSlash := strings.LastIndex(image, "/")
	if lastColon > lastSlash {
		baseImage = image[:lastColon]
	}
	if baseImage == "" {
		baseImage = image
	}

	// Canonical tag: g<shortsha>
	gitTag := ""
	gitImage := ""
	if shortSHA != "" {
		gitTag = "g" + shortSHA
		gitImage = baseImage + ":" + gitTag
	}

	buildImage := image
	latestImage := baseImage + ":latest"

	registryHost := ""
	if ctxData.Registry != nil {
		registryHost = platformutils.NormalizeRegistryHost(ctxData.Registry.RegistryUrl)
	}
	if registryHost == "" {
		registryHost = platformutils.NormalizeRegistryHost(platformutils.RegistryFromImage(image))
	}
	if registryHost == "" {
		registryHost = "docker.io"
	}

	if ctxData.Registry != nil && ctxData.Registry.Username != "" && ctxData.Registry.Password != "" {
		log.Printf("[worker] authenticating registry %s with user=%s credential_id=%s password_set=%t",
			registryHost,
			ctxData.Registry.Username,
			ctxData.Registry.ID,
			ctxData.Registry.Password != "",
		)

		if err := platformutils.DockerLogin(ctx, registryHost, ctxData.Registry.Username, ctxData.Registry.Password); err != nil {
			return err
		}
	}

	dockerfilePath := ctxData.Service.DockerfilePath
	if dockerfilePath == "" {
		dockerfilePath = "Dockerfile"
	}
	contextPath := ctxData.Service.DockerContext
	if contextPath == "" {
		contextPath = "."
	}

	log.Printf("[worker] building image %s", buildImage)
	if logger != nil {
		logger.Logf(ctx, "docker build %s", buildImage)
	}
	buildArgs := []string{
		"build",
		"-t", buildImage,
		"-f", filepath.Join(workDir, dockerfilePath),
		filepath.Join(workDir, contextPath),
	}
	buildArgs = appendDockerBuildProxyArgs(buildArgs)
	buildArgs = appendDockerBuildNetworkArg(buildArgs)
	if err := platformutils.RunCommandWithLogger(ctx, workDir, "docker", buildArgs, nil, logger); err != nil {
		return err
	}
	if logger != nil {
		logger.Flush(ctx)
	}

	if latestImage != "" && latestImage != buildImage {
		log.Printf("[worker] tagging image %s", latestImage)
		if logger != nil {
			logger.Logf(ctx, "docker tag %s %s", buildImage, latestImage)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workDir, "docker", []string{"tag", buildImage, latestImage}, nil, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	if gitImage != "" && gitImage != buildImage {
		log.Printf("[worker] tagging image %s", gitImage)
		if logger != nil {
			logger.Logf(ctx, "docker tag %s %s", buildImage, gitImage)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workDir, "docker", []string{"tag", buildImage, gitImage}, nil, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	// Push canonical git tag first (this is the version we deploy)
	if gitImage != "" {
		log.Printf("[worker] pushing image %s", gitImage)
		if logger != nil {
			logger.Logf(ctx, "docker push %s", gitImage)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workDir, "docker", []string{"push", gitImage}, nil, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	// Push original tag
	if buildImage != gitImage {
		log.Printf("[worker] pushing image %s", buildImage)
		if logger != nil {
			logger.Logf(ctx, "docker push %s", buildImage)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workDir, "docker", []string{"push", buildImage}, nil, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	if latestImage != "" && latestImage != buildImage && latestImage != gitImage {
		log.Printf("[worker] pushing image %s", latestImage)
		if logger != nil {
			logger.Logf(ctx, "docker push %s", latestImage)
		}
		if err := platformutils.RunCommandWithLogger(ctx, workDir, "docker", []string{"push", latestImage}, nil, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	// Capture OCI digest after push
	digest := ""
	pushRef := gitImage
	if pushRef == "" {
		pushRef = buildImage
	}
	digestOutput, digestErr := platformutils.RunCommandOutput(ctx, workDir, "docker", []string{"inspect", "--format", "{{index .RepoDigests 0}}", pushRef}, nil)
	if digestErr == nil && strings.Contains(digestOutput, "@sha256:") {
		parts := strings.SplitN(digestOutput, "@", 2)
		if len(parts) == 2 {
			digest = parts[1]
		}
	}
	if logger != nil && digest != "" {
		logger.Logf(ctx, "image digest: %s", digest)
	}

	// Register build in API
	if client != nil && tokens != nil && gitTag != "" {
		registerBuildInAPI(ctx, client, cfg, tokens, ctxData.Service.ID, commitSHA, shortSHA, gitTag, digest, environment, pushRef)
	}

	// Update service DockerImage to use the versioned tag for deploy
	if gitImage != "" {
		ctxData.Service.DockerImage = gitImage
	}

	return nil
}

func registerBuildInAPI(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, serviceID, commit, shortSha, tag, digest, environment, image string) {
	payload := map[string]string{
		"serviceId":   serviceID,
		"commit":      commit,
		"shortSha":    shortSha,
		"tag":         tag,
		"digest":      digest,
		"environment": environment,
		"image":       image,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[worker] failed to marshal build registration: %v", err)
		return
	}
	endpoint := cfg.ApiBaseURL + "/workers/builds"
	if err := platformops.DoJSONRequest(ctx, client, cfg, tokens, "POST", endpoint, body, nil, "build registration"); err != nil {
		log.Printf("[worker] failed to register build: %v", err)
	}
}

func appendDockerBuildProxyArgs(args []string) []string {
	proxyKeys := []string{
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"NO_PROXY",
		"http_proxy",
		"https_proxy",
		"no_proxy",
	}
	for _, key := range proxyKeys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			args = append(args, "--build-arg", fmt.Sprintf("%s=%s", key, value))
		}
	}
	return args
}

func appendDockerBuildNetworkArg(args []string) []string {
	if network, ok := os.LookupEnv("DOCKER_BUILD_NETWORK"); ok && network != "" {
		args = append(args, "--network", network)
	}
	return args
}

func normalizeTargetCommitRef(value string) string {
	ref := strings.TrimSpace(value)
	if ref == "" {
		return ""
	}

	switch strings.ToLower(ref) {
	case "latest", "head", "origin/head":
		return ""
	}

	if strings.HasPrefix(strings.ToLower(ref), "deploy-") || strings.HasPrefix(strings.ToLower(ref), "op-") {
		return ""
	}

	return ref
}
