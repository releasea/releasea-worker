package shared

import (
	"releaseaworker/internal/models"
	"strings"

	commonenv "releaseaworker/internal/modules/shared/env"
	"releaseaworker/internal/modules/shared/namespaces"
)

func ResolvePort(value int) int {
	if value <= 0 {
		return 80
	}
	return value
}

func ResolveNamespace(_ models.Config, environment string) string {
	return namespaces.ResolveAppNamespace(environment)
}

func ValidateAppNamespace(namespace string) error {
	return namespaces.ValidateAppNamespace(namespace)
}

func NormalizeNamespace(value string) string {
	name := ToKubeName(value)
	if name == "" {
		return "releasea-apps-prod"
	}
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
		if name == "" {
			return "releasea-apps-prod"
		}
	}
	return name
}

func ToKubeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "_", "-")
	value = strings.ReplaceAll(value, " ", "-")
	value = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}, value)
	value = strings.Trim(value, "-")
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return value
}

func UniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func HasHostSuffix(hosts []string, suffix string) bool {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return false
	}
	for _, host := range hosts {
		trimmed := strings.TrimSpace(host)
		if trimmed == "" {
			continue
		}
		if strings.HasSuffix(trimmed, suffix) {
			return true
		}
	}
	return false
}

func EnvInt(key string, fallback int) int {
	return commonenv.Int(key, fallback)
}
