# ──────────────────────── build stage ──────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder
ARG BUILDX_VERSION=0.24.0
ARG TARGETOS
ARG TARGETARCH
RUN apk add --no-cache git curl
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN arch="${TARGETARCH:-}"; \
    if [ -z "${arch}" ]; then \
      case "$(uname -m)" in \
        aarch64|arm64) arch="arm64" ;; \
        x86_64|amd64) arch="amd64" ;; \
        *) arch="$(go env GOARCH)" ;; \
      esac; \
    fi; \
    CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${arch}" \
      go build -trimpath -ldflags="-s -w" -o /out/releasea-worker ./cmd/main.go
RUN mkdir -p /out/cli-plugins && \
    arch="${TARGETARCH:-}" && \
    if [ -z "${arch}" ]; then \
      case "$(uname -m)" in \
        aarch64|arm64) arch="arm64" ;; \
        x86_64|amd64) arch="amd64" ;; \
        *) arch="amd64" ;; \
      esac; \
    fi && \
    curl -fsSL -o /out/cli-plugins/docker-buildx \
      "https://github.com/docker/buildx/releases/download/v${BUILDX_VERSION}/buildx-v${BUILDX_VERSION}.linux-${arch}" && \
    chmod +x /out/cli-plugins/docker-buildx
RUN arch="${TARGETARCH:-}" && \
    if [ -z "${arch}" ]; then \
      case "$(uname -m)" in \
        aarch64|arm64) arch="arm64" ;; \
        x86_64|amd64) arch="amd64" ;; \
        *) arch="amd64" ;; \
      esac; \
    fi && \
    curl -fsSL -o /out/mc "https://dl.min.io/client/mc/release/linux-${arch}/mc" && \
    chmod +x /out/mc

# ────────────────────── runtime stage ──────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache \
        ca-certificates \
        git \
        docker-cli \
        kubectl \
        python3 \
        py3-pip \
        nodejs \
        npm

WORKDIR /app
COPY --from=builder /out/releasea-worker ./releasea-worker
COPY --from=builder /out/cli-plugins/docker-buildx /usr/local/libexec/docker/cli-plugins/docker-buildx
COPY --from=builder /out/mc /usr/local/bin/mc

ENV DOCKER_HOST=tcp://localhost:2375 \
    DOCKER_BUILDKIT=1 \
    DOCKER_CLI_EXPERIMENTAL=enabled

ENTRYPOINT ["./releasea-worker"]
