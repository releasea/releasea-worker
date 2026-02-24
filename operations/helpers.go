package operations

import (
	"strings"

	commonenv "releaseaworker/common/env"
	commonsource "releaseaworker/common/source"
	commonstr "releaseaworker/common/strutil"
	commonvalues "releaseaworker/common/values"
	"releaseaworker/namespaces"
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
	return commonstr.ToKubeName(value)
}

func uniqueStrings(values []string) []string {
	return commonstr.UniqueStrings(values)
}

func hasHostSuffix(hosts []string, suffix string) bool {
	return commonstr.HasHostSuffix(hosts, suffix)
}

func normalizeSourceType(sourceType string) string {
	return commonsource.NormalizeType(sourceType)
}

func mapValue(value interface{}) map[string]interface{} {
	return commonvalues.MapValue(value)
}

func stringValue(source map[string]interface{}, key string) string {
	return commonvalues.StringValue(source, key)
}

func envInt(key string, fallback int) int {
	return commonenv.Int(key, fallback)
}
