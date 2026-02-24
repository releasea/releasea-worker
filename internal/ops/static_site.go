package ops

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultStaticOutputDir = "dist"
const defaultStaticCacheTTL = 3600

func handleStaticSiteDeploy(ctx context.Context, cfg Config, ctxData deployContext, environment string, logger *deployLogger) error {
	if ctxData.Service.RepoURL == "" {
		return errors.New("repository URL not set")
	}

	workspace, err := os.MkdirTemp("", "releasea-worker-static-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace)

	repoURL := ctxData.Service.RepoURL
	if ctxData.SCM != nil && ctxData.SCM.Token != "" {
		repoURL = injectToken(repoURL, ctxData.SCM)
	}

	cloneArgs := []string{"clone", "--depth", "1"}
	if ctxData.Service.Branch != "" {
		cloneArgs = append(cloneArgs, "--branch", ctxData.Service.Branch)
	}
	cloneArgs = append(cloneArgs, repoURL, workspace)

	log.Printf("[worker] cloning repository for static site %s", ctxData.Service.Name)
	if logger != nil {
		logger.Logf(ctx, "cloning repository %s", ctxData.Service.RepoURL)
	}
	if err := runCommandWithLogger(ctx, workspace, "git", cloneArgs, nil, logger); err != nil {
		return err
	}
	if logger != nil {
		logger.Flush(ctx)
	}

	workDir := workspace
	if ctxData.Service.RootDir != "" {
		workDir = filepath.Join(workspace, ctxData.Service.RootDir)
	}

	if installCommand := strings.TrimSpace(ctxData.Service.InstallCommand); installCommand != "" {
		log.Printf("[worker] running install command for static site %s", ctxData.Service.Name)
		if logger != nil {
			logger.Logf(ctx, "running install command")
		}
		if err := runShellWithLogger(ctx, workDir, installCommand, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	if buildCommand := strings.TrimSpace(ctxData.Service.BuildCommand); buildCommand != "" {
		log.Printf("[worker] running build command for static site %s", ctxData.Service.Name)
		if logger != nil {
			logger.Logf(ctx, "running build command")
		}
		if err := runShellWithLogger(ctx, workDir, buildCommand, logger); err != nil {
			return err
		}
		if logger != nil {
			logger.Flush(ctx)
		}
	}

	outputDir := resolveStaticOutputDir(ctxData.Service.OutputDir)
	outputPath := filepath.Join(workDir, outputDir)
	info, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("output directory not found: %s", outputPath)
	}
	if !info.IsDir() {
		return fmt.Errorf("output path is not a directory: %s", outputPath)
	}

	alias, err := ensureMinioAlias(ctx, cfg, logger)
	if err != nil {
		return err
	}

	bucket := strings.TrimSpace(cfg.MinioBucket)
	if bucket == "" {
		return errors.New("minio bucket not configured")
	}

	sitePrefix := staticSitePrefix(cfg, ctxData.Service)
	cacheControl := cacheControlValue(ctxData.Service.CacheTTL)

	if logger != nil {
		logger.Logf(ctx, "syncing static site to bucket=%s prefix=%s env=%s", bucket, sitePrefix, environment)
	}

	if err := ensureMinioBucket(ctx, alias, bucket, logger); err != nil {
		return err
	}
	if err := setBucketPublic(ctx, alias, bucket, logger); err != nil {
		return err
	}
	if err := mirrorStaticSite(ctx, alias, bucket, sitePrefix, outputPath, cacheControl, logger); err != nil {
		return err
	}

	return nil
}

func resolveStaticOutputDir(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultStaticOutputDir
	}
	return value
}

func staticSitePrefix(cfg Config, svc serviceConfig) string {
	name := toKubeName(svc.Name)
	if name == "" {
		name = toKubeName(svc.ID)
	}
	if name == "" {
		name = "site"
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.StaticSitePrefix), "/")
	if prefix == "" {
		return name
	}
	return path.Join(prefix, name)
}

func cacheControlValue(raw string) string {
	ttl := parseCacheTTL(raw)
	if ttl <= 0 {
		return "no-cache"
	}
	return fmt.Sprintf("public, max-age=%d", ttl)
}

func parseCacheTTL(raw string) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultStaticCacheTTL
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultStaticCacheTTL
	}
	if parsed < 0 {
		return 0
	}
	return parsed
}

func ensureMinioAlias(ctx context.Context, cfg Config, logger *deployLogger) (string, error) {
	endpoint := strings.TrimSpace(cfg.MinioEndpoint)
	if endpoint == "" {
		return "", errors.New("minio endpoint not configured")
	}
	accessKey := strings.TrimSpace(cfg.MinioAccessKey)
	secretKey := strings.TrimSpace(cfg.MinioSecretKey)
	if accessKey == "" || secretKey == "" {
		return "", errors.New("minio credentials not configured")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		scheme := "http"
		if cfg.MinioSecure {
			scheme = "https"
		}
		endpoint = scheme + "://" + endpoint
	}
	alias := "releasea-minio"
	args := []string{"alias", "set", alias, endpoint, accessKey, secretKey}
	if logger != nil {
		logger.Logf(ctx, "configuring minio alias %s", alias)
	}
	if err := runCommandWithLogger(ctx, "", "mc", args, nil, logger); err != nil {
		return "", err
	}
	return alias, nil
}

func ensureMinioBucket(ctx context.Context, alias, bucket string, logger *deployLogger) error {
	target := fmt.Sprintf("%s/%s", alias, bucket)
	args := []string{"mb", "--ignore-existing", target}
	if logger != nil {
		logger.Logf(ctx, "ensuring bucket %s", target)
	}
	return runCommandWithLogger(ctx, "", "mc", args, nil, logger)
}

func setBucketPublic(ctx context.Context, alias, bucket string, logger *deployLogger) error {
	target := fmt.Sprintf("%s/%s", alias, bucket)
	if logger != nil {
		logger.Logf(ctx, "setting bucket policy to public read (%s)", target)
	}
	if err := runCommandWithLogger(ctx, "", "mc", []string{"anonymous", "set", "download", target}, nil, logger); err == nil {
		return nil
	}
	return runCommandWithLogger(ctx, "", "mc", []string{"policy", "set", "download", target}, nil, logger)
}

func mirrorStaticSite(ctx context.Context, alias, bucket, prefix, dir, cacheControl string, logger *deployLogger) error {
	target := fmt.Sprintf("%s/%s/%s", alias, bucket, strings.Trim(prefix, "/"))
	args := []string{"mirror", "--overwrite", "--remove"}
	attr := cacheControlAttr(cacheControl)
	if attr != "" {
		args = append(args, "--attr", attr)
	}
	args = append(args, dir, target)
	if logger != nil {
		logger.Logf(ctx, "uploading static assets to %s", target)
	}
	if err := runCommandWithLogger(ctx, "", "mc", args, nil, logger); err != nil {
		if attr == "" {
			return err
		}
		if logger != nil {
			logger.Logf(ctx, "mc mirror failed with attr, retrying without cache metadata")
		}
		args = []string{"mirror", "--overwrite", "--remove", dir, target}
		return runCommandWithLogger(ctx, "", "mc", args, nil, logger)
	}
	return nil
}

func cacheControlAttr(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return fmt.Sprintf("Cache-Control=%s", value)
}

func deleteStaticSiteAssets(ctx context.Context, cfg Config, svc serviceConfig) error {
	alias, err := ensureMinioAlias(ctx, cfg, nil)
	if err != nil {
		return err
	}
	bucket := strings.TrimSpace(cfg.MinioBucket)
	if bucket == "" {
		return errors.New("minio bucket not configured")
	}
	prefix := staticSitePrefix(cfg, svc)
	target := fmt.Sprintf("%s/%s/%s", alias, bucket, strings.Trim(prefix, "/"))
	args := []string{"rm", "--recursive", "--force", target}
	return runCommandWithLogger(ctx, "", "mc", args, nil, nil)
}
