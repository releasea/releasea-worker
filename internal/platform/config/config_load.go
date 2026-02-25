package config

import (
	"strings"
	"time"

	commonenv "releaseaworker/internal/platform/shared"
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
	apiBase := commonenv.String("RELEASEA_API_BASE_URL", "http://host.k3d.internal:8070/api/v1")
	apiBase = strings.TrimRight(apiBase, "/")
	intervalSeconds := commonenv.Int("HEARTBEAT_INTERVAL_SECONDS", 30)
	if intervalSeconds <= 0 {
		intervalSeconds = 30
	}

	cfg := Config{
		ApiBaseURL:            apiBase,
		Token:                 commonenv.String("RELEASEA_WORKER_TOKEN", ""),
		WorkerID:              commonenv.String("WORKER_ID", ""),
		WorkerName:            commonenv.String("WORKER_NAME", "releasea-worker"),
		Environment:           commonenv.String("WORKER_ENVIRONMENT", "production"),
		Namespace:             commonenv.String("WORKER_NAMESPACE", "default"),
		NamespacePrefix:       commonenv.String("WORKER_NAMESPACE_PREFIX", ""),
		Cluster:               commonenv.String("WORKER_CLUSTER", "k3d-local"),
		Version:               commonenv.String("WORKER_VERSION", "dev"),
		Tags:                  parseTags(commonenv.String("WORKER_TAGS", "")),
		HeartbeatInterval:     time.Duration(intervalSeconds) * time.Second,
		RabbitURL:             commonenv.String("RABBITMQ_URL", ""),
		QueueName:             commonenv.String("WORKER_QUEUE", "releasea.worker"),
		DeploymentName:        commonenv.String("WORKER_DEPLOYMENT_NAME", ""),
		DeploymentNamespace:   commonenv.String("WORKER_DEPLOYMENT_NAMESPACE", commonenv.String("WORKER_NAMESPACE", "default")),
		DesiredAgentsFallback: commonenv.Int("WORKER_DESIRED_AGENTS", 0),
		InternalDomain:        commonenv.String("RELEASEA_INTERNAL_DOMAIN", "releasea.internal"),
		ExternalDomain:        commonenv.String("RELEASEA_EXTERNAL_DOMAIN", "releasea.external"),
		InternalGateway:       commonenv.String("RELEASEA_INTERNAL_GATEWAY", "istio-system/releasea-internal-gateway"),
		ExternalGateway:       commonenv.String("RELEASEA_EXTERNAL_GATEWAY", "istio-system/releasea-external-gateway"),
		MinioEndpoint:         commonenv.String("RELEASEA_MINIO_ENDPOINT", "releasea-minio.releasea-system.svc.cluster.local:9000"),
		MinioAccessKey:        commonenv.String("RELEASEA_MINIO_ACCESS_KEY", "releasea"),
		MinioSecretKey:        commonenv.String("RELEASEA_MINIO_SECRET_KEY", "releaseaadmin"),
		MinioBucket:           commonenv.String("RELEASEA_MINIO_BUCKET", "releasea-static"),
		MinioSecure:           commonenv.Bool("RELEASEA_MINIO_SECURE", false),
		StaticSitePrefix:      commonenv.String("RELEASEA_STATIC_SITE_PREFIX", "sites"),
		StaticNginxService:    commonenv.String("RELEASEA_STATIC_NGINX_SERVICE", "releasea-static-nginx"),
		StaticNginxNamespace:  commonenv.String("RELEASEA_STATIC_NGINX_NAMESPACE", "releasea-system"),
		PollInterval:          time.Duration(commonenv.Int("WORKER_POLL_SECONDS", 20)) * time.Second,
		PollBatchLimit:        commonenv.Int("WORKER_POLL_LIMIT", 10),
	}
	if cfg.WorkerID == "" {
		cfg.WorkerID = cfg.WorkerName
	}
	if cfg.WorkerName == "" {
		cfg.WorkerName = cfg.WorkerID
	}
	return cfg
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
