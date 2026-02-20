package ops

import (
	"os"
	"strconv"
	"strings"

	"releaseaworker/internal/worker/namespaces"
)

func resolvePort(value int) int {
	if value <= 0 {
		return 80
	}
	return value
}

func resolveNamespace(_ Config, environment string) string {
	return namespaces.ResolveAppNamespace(environment)
}

func validateAppNamespace(namespace string) error {
	return namespaces.ValidateAppNamespace(namespace)
}

func normalizeNamespace(value string) string {
	name := toKubeName(value)
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

func toKubeName(value string) string {
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

func uniqueStrings(values []string) []string {
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

func hasHostSuffix(hosts []string, suffix string) bool {
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

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
