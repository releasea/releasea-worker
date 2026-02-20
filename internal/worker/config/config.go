package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ApiBaseURL            string
	Token                 string
	WorkerID              string
	WorkerName            string
	Environment           string
	Namespace             string
	NamespacePrefix       string
	Cluster               string
	Version               string
	Tags                  []string
	HeartbeatInterval     time.Duration
	RabbitURL             string
	QueueName             string
	DeploymentName        string
	DeploymentNamespace   string
	DesiredAgentsFallback int
	InternalDomain        string
	ExternalDomain        string
	InternalGateway       string
	ExternalGateway       string
	MinioEndpoint         string
	MinioAccessKey        string
	MinioSecretKey        string
	MinioBucket           string
	MinioSecure           bool
	StaticSitePrefix      string
	StaticNginxService    string
	StaticNginxNamespace  string
	PollInterval          time.Duration
	PollBatchLimit        int
}

func Load() Config {
	apiBase := env("RELEASEA_API_BASE_URL", "http://host.k3d.internal:8070/api/v1")
	apiBase = strings.TrimRight(apiBase, "/")
	intervalSeconds := envInt("HEARTBEAT_INTERVAL_SECONDS", 30)
	if intervalSeconds <= 0 {
		intervalSeconds = 30
	}

	cfg := Config{
		ApiBaseURL:            apiBase,
		Token:                 env("RELEASEA_WORKER_TOKEN", ""),
		WorkerID:              env("WORKER_ID", ""),
		WorkerName:            env("WORKER_NAME", "releasea-worker"),
		Environment:           env("WORKER_ENVIRONMENT", "production"),
		Namespace:             env("WORKER_NAMESPACE", "default"),
		NamespacePrefix:       env("WORKER_NAMESPACE_PREFIX", ""),
		Cluster:               env("WORKER_CLUSTER", "k3d-local"),
		Version:               env("WORKER_VERSION", "dev"),
		Tags:                  parseTags(env("WORKER_TAGS", "")),
		HeartbeatInterval:     time.Duration(intervalSeconds) * time.Second,
		RabbitURL:             env("RABBITMQ_URL", ""),
		QueueName:             env("WORKER_QUEUE", "releasea.worker"),
		DeploymentName:        env("WORKER_DEPLOYMENT_NAME", ""),
		DeploymentNamespace:   env("WORKER_DEPLOYMENT_NAMESPACE", env("WORKER_NAMESPACE", "default")),
		DesiredAgentsFallback: envInt("WORKER_DESIRED_AGENTS", 0),
		InternalDomain:        env("RELEASEA_INTERNAL_DOMAIN", "releasea.internal"),
		ExternalDomain:        env("RELEASEA_EXTERNAL_DOMAIN", "releasea.external"),
		InternalGateway:       env("RELEASEA_INTERNAL_GATEWAY", "istio-system/releasea-internal-gateway"),
		ExternalGateway:       env("RELEASEA_EXTERNAL_GATEWAY", "istio-system/releasea-external-gateway"),
		MinioEndpoint:         env("RELEASEA_MINIO_ENDPOINT", "releasea-minio.releasea-system.svc.cluster.local:9000"),
		MinioAccessKey:        env("RELEASEA_MINIO_ACCESS_KEY", "releasea"),
		MinioSecretKey:        env("RELEASEA_MINIO_SECRET_KEY", "releaseaadmin"),
		MinioBucket:           env("RELEASEA_MINIO_BUCKET", "releasea-static"),
		MinioSecure:           envBool("RELEASEA_MINIO_SECURE", false),
		StaticSitePrefix:      env("RELEASEA_STATIC_SITE_PREFIX", "sites"),
		StaticNginxService:    env("RELEASEA_STATIC_NGINX_SERVICE", "releasea-static-nginx"),
		StaticNginxNamespace:  env("RELEASEA_STATIC_NGINX_NAMESPACE", "releasea-system"),
		PollInterval:          time.Duration(envInt("WORKER_POLL_SECONDS", 20)) * time.Second,
		PollBatchLimit:        envInt("WORKER_POLL_LIMIT", 10),
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = cfg.WorkerName
	}
	if cfg.WorkerName == "" {
		cfg.WorkerName = cfg.WorkerID
	}
	return cfg
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	value = strings.ToLower(value)
	switch value {
	case "true", "1", "yes", "y":
		return true
	case "false", "0", "no", "n":
		return false
	default:
		return fallback
	}
}

func parseTags(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
