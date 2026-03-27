package registryproviders

import (
	"net/url"
	"strings"
)

type Definition struct {
	ID          string
	DefaultHost string
}

type Runtime interface {
	ID() string
	ResolveLoginHost(registryURL, image string) string
}

var registry = map[string]Definition{
	"docker": {ID: "docker", DefaultHost: "docker.io"},
	"ghcr":   {ID: "ghcr", DefaultHost: "ghcr.io"},
	"ecr":    {ID: "ecr"},
	"gcr":    {ID: "gcr", DefaultHost: "gcr.io"},
	"acr":    {ID: "acr"},
}

func Normalize(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "docker"
	}
	return provider
}

func Resolve(provider string) (Definition, bool) {
	definition, ok := registry[Normalize(provider)]
	return definition, ok
}

func ResolveRuntime(provider string) Runtime {
	definition, ok := Resolve(provider)
	if !ok {
		definition = Definition{ID: Normalize(provider), DefaultHost: "docker.io"}
	}
	return runtime{definition: definition}
}

func NormalizeHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			value = parsed.Host
		}
	}
	value = strings.TrimSuffix(value, "/v1/")
	value = strings.TrimSuffix(value, "/")
	if value == "index.docker.io" {
		return "docker.io"
	}
	return value
}

func HostFromImage(image string) string {
	if image == "" {
		return ""
	}
	parts := strings.Split(image, "/")
	if len(parts) < 2 {
		return ""
	}
	host := parts[0]
	if strings.Contains(host, ".") || strings.Contains(host, ":") {
		return host
	}
	return ""
}

func ResolveLoginHost(provider, registryURL, image string) string {
	return ResolveRuntime(provider).ResolveLoginHost(registryURL, image)
}

type runtime struct {
	definition Definition
}

func (r runtime) ID() string {
	return r.definition.ID
}

func (r runtime) ResolveLoginHost(registryURL, image string) string {
	if host := NormalizeHost(registryURL); host != "" {
		return host
	}
	if host := NormalizeHost(HostFromImage(image)); host != "" {
		return host
	}
	if host := NormalizeHost(r.definition.DefaultHost); host != "" {
		return host
	}
	return "docker.io"
}
