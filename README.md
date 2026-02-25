# Releasea Worker

Distributed worker that executes deploy and runtime operations inside Kubernetes clusters.

## Overview

The worker consumes queue messages, applies desired state, reports runtime health, manages traffic strategy transitions, and synchronizes operation status back to the API.

## Running Locally

```bash
go mod download
go run ./cmd/main.go
```

## Environment Variables

### API and Identity

| Variable | Description | Default |
|---|---|---|
| `RELEASEA_API_BASE_URL` | Releasea API base URL (without trailing slash) | `http://host.k3d.internal:8070/api/v1` |
| `RELEASEA_WORKER_TOKEN` | Worker authentication token | _(empty)_ |
| `WORKER_ID` | Unique worker ID | `releasea-runner-prod-01` |
| `WORKER_NAME` | Worker display name | `releasea-runner-prod-01` |
| `WORKER_ENVIRONMENT` | Environment this worker serves | `production` |
| `WORKER_NAMESPACE` | Worker namespace | `releasea-workers-prod-01` |
| `WORKER_NAMESPACE_PREFIX` | Namespace prefix used for generated app namespaces | `releasea-workers` |
| `WORKER_CLUSTER` | Cluster identifier | `k3d-local` |
| `WORKER_VERSION` | Worker semantic/runtime version string | `dev` |
| `WORKER_TAGS` | Comma-separated worker tags | `deploy,approved` |

### Queue and Heartbeat

| Variable | Description | Default |
|---|---|---|
| `RABBITMQ_URL` | RabbitMQ AMQP URL | `amqp://releasea:releasea@localhost:5672/` |
| `WORKER_QUEUE` | Queue consumed by this worker | `releasea.worker` |
| `HEARTBEAT_INTERVAL_SECONDS` | Heartbeat interval sent to API | `30` |

### Polling and Runtime Monitoring

| Variable | Description | Default |
|---|---|---|
| `WORKER_POLL_SECONDS` | Polling interval for operation fetch loop | `20` |
| `WORKER_POLL_LIMIT` | Max operations fetched per polling cycle | `10` |
| `WORKER_RUNTIME_SECONDS` | Runtime monitor interval | `5` |
| `WORKER_DESIRED_AGENTS` | Fallback desired agent count | `0` |

### Deploy Execution

| Variable | Description | Default |
|---|---|---|
| `WORKER_DEPLOY_RETRY_DELAY_SECONDS` | Delay between deploy retries | `6` |
| `WORKER_DEPLOY_RETRY_MAX_ATTEMPTS` | Max deploy retry attempts | `3` |
| `WORKER_DEPLOY_READY_TIMEOUT_SECONDS` | Deploy readiness timeout | `420` |
| `WORKER_DEPLOY_READY_POLL_SECONDS` | Readiness poll interval | `5` |
| `WORKER_BLUE_GREEN_OBSERVATION_SECONDS` | Blue/green post-promotion observation window | `30` |

### Auto-Deploy Monitor

| Variable | Description | Default |
|---|---|---|
| `WORKER_AUTODEPLOY_ENABLED` | Enables repository auto-deploy monitor | `1` |
| `WORKER_AUTODEPLOY_SECONDS` | Auto-deploy scan interval | `60` |
| `WORKER_AUTODEPLOY_LEASE_SECONDS` | Lease TTL for auto-deploy lock ownership | `120` |
| `WORKER_AUTODEPLOY_ERROR_COOLDOWN_SECONDS` | Cooldown after processing errors | `60` |
| `WORKER_AUTODEPLOY_QUEUE_ERROR_COOLDOWN_SECONDS` | Cooldown after queue errors | `30` |
| `WORKER_AUTODEPLOY_PENDING_SECONDS` | Pending-window guard to avoid duplicate queueing | `130` |

### Pause When Idle

| Variable | Description | Default |
|---|---|---|
| `WORKER_PAUSE_IDLE_DEFAULT_SECONDS` | Inactivity threshold before scale-to-zero | `3600` |
| `WORKER_PAUSE_IDLE_RESUME_WINDOW_SECONDS` | Resume stabilization window | `120` |

### Curator

| Variable | Description | Default |
|---|---|---|
| `WORKER_CURATOR_SECONDS` | Curator sweep interval | `60` |
| `WORKER_CURATOR_MAX_SECONDS` | Max age for curator cleanup targets | `600` |

### Routing and Domains

| Variable | Description | Default |
|---|---|---|
| `RELEASEA_INTERNAL_DOMAIN` | Internal base domain | `releasea.internal` |
| `RELEASEA_EXTERNAL_DOMAIN` | External base domain | `releasea.external` |
| `RELEASEA_INTERNAL_GATEWAY` | Internal gateway reference | `istio-system/releasea-internal-gateway` |
| `RELEASEA_EXTERNAL_GATEWAY` | External gateway reference | `istio-system/releasea-external-gateway` |
| `RELEASEA_STATIC_SITE_PREFIX` | Prefix used for static site object keys | `sites` |
| `RELEASEA_STATIC_NGINX_SERVICE` | Shared static nginx service name | `releasea-static-nginx` |
| `RELEASEA_STATIC_NGINX_NAMESPACE` | Namespace of shared static nginx service | `releasea-system` |
| `RELEASEA_NAMESPACE_MAPPING` | Optional JSON override for environment→namespace map | _(empty)_ |

### Optional Deployment Metadata

| Variable | Description | Default |
|---|---|---|
| `WORKER_DEPLOYMENT_NAME` | Worker Kubernetes deployment name | _(empty)_ |
| `WORKER_DEPLOYMENT_NAMESPACE` | Worker deployment namespace | _(empty)_ |

### MinIO (Static Artifacts)

| Variable | Description | Default |
|---|---|---|
| `RELEASEA_MINIO_ENDPOINT` | MinIO endpoint | `releasea-minio.releasea-system.svc.cluster.local:9000` |
| `RELEASEA_MINIO_ACCESS_KEY` | MinIO access key | `releasea` |
| `RELEASEA_MINIO_SECRET_KEY` | MinIO secret key | `releaseaadmin` |
| `RELEASEA_MINIO_BUCKET` | MinIO bucket for static artifacts | `releasea-static` |
| `RELEASEA_MINIO_SECURE` | Enables HTTPS to MinIO | `false` |

### RabbitMQ TLS (Optional)

| Variable | Description | Default |
|---|---|---|
| `RABBITMQ_TLS_ENABLE` | Enables TLS for RabbitMQ connection | `false` |
| `RABBITMQ_TLS_SERVER_NAME` | TLS server name override | _(empty)_ |
| `RABBITMQ_TLS_CA_PATH` | CA bundle path | _(empty)_ |
| `RABBITMQ_TLS_CERT_PATH` | Client cert path | _(empty)_ |
| `RABBITMQ_TLS_KEY_PATH` | Client key path | _(empty)_ |
| `RABBITMQ_TLS_INSECURE` | Skip TLS verification (dev only) | `false` |

## License

Apache 2.0 - See [LICENSE](LICENSE) for details.
