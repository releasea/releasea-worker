package namespaces

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Fixed application namespaces. All app workloads MUST deploy into one of these.
const (
	NamespaceProduction  = "releasea-apps-production"
	NamespaceStaging     = "releasea-apps-staging"
	NamespaceDevelopment = "releasea-apps-development"
	NamespaceSystem      = "releasea-system"
)

// defaultNamespaceMapping maps environment names to fixed namespaces.
// "production-like" -> releasea-apps-production
// "staging-like"    -> releasea-apps-staging
// everything else   -> releasea-apps-development
var defaultNamespaceMapping = map[string]string{
	"prod":       NamespaceProduction,
	"production": NamespaceProduction,
	"live":       NamespaceProduction,

	"staging":     NamespaceStaging,
	"stage":       NamespaceStaging,
	"pre-prod":    NamespaceStaging,
	"preprod":     NamespaceStaging,
	"uat":         NamespaceStaging,
	"pre-release": NamespaceStaging,

	"dev":         NamespaceDevelopment,
	"development": NamespaceDevelopment,
	"qa":          NamespaceDevelopment,
	"sandbox":     NamespaceDevelopment,
	"test":        NamespaceDevelopment,
	"testing":     NamespaceDevelopment,
	"preview":     NamespaceDevelopment,
	"feature":     NamespaceDevelopment,
	"ci":          NamespaceDevelopment,
	"local":       NamespaceDevelopment,
}

// loadNamespaceMapping returns the mapping, merging any overrides from
// the RELEASEA_NAMESPACE_MAPPING env var (JSON object: {"env": "namespace"}).
func loadNamespaceMapping() map[string]string {
	merged := make(map[string]string, len(defaultNamespaceMapping))
	for k, v := range defaultNamespaceMapping {
		merged[k] = v
	}

	raw := strings.TrimSpace(os.Getenv("RELEASEA_NAMESPACE_MAPPING"))
	if raw == "" {
		return merged
	}

	var overrides map[string]string
	if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
		return merged
	}
	for env, ns := range overrides {
		env = strings.TrimSpace(strings.ToLower(env))
		ns = strings.TrimSpace(ns)
		if env != "" && ns != "" && isValidAppNamespace(ns) {
			merged[env] = ns
		}
	}
	return merged
}

// resolveAppNamespace maps an environment name to one of the three fixed
// application namespaces. This is the SINGLE SOURCE OF TRUTH for namespace
// resolution across the entire platform.
//
// Rules:
//  1. Empty environment defaults to "prod" -> releasea-apps-production.
//  2. Known environment names are looked up in the mapping.
//  3. Unknown environments fall back to releasea-apps-development.
//  4. The result is NEVER releasea-system.
func resolveAppNamespace(environment string) string {
	environment = strings.TrimSpace(strings.ToLower(environment))
	if environment == "" {
		environment = "prod"
	}

	mapping := loadNamespaceMapping()
	if ns, ok := mapping[environment]; ok {
		return ns
	}

	return NamespaceDevelopment
}

// ResolveAppNamespace is the exported wrapper for resolveAppNamespace.
func ResolveAppNamespace(environment string) string {
	return resolveAppNamespace(environment)
}

// isValidAppNamespace returns true only for the three allowed app namespaces.
func isValidAppNamespace(namespace string) bool {
	switch namespace {
	case NamespaceProduction, NamespaceStaging, NamespaceDevelopment:
		return true
	default:
		return false
	}
}

// IsValidAppNamespace reports whether the namespace is one of the allowed app namespaces.
func IsValidAppNamespace(namespace string) bool {
	return isValidAppNamespace(namespace)
}

// isSystemNamespace returns true if the namespace is the reserved system namespace.
func isSystemNamespace(namespace string) bool {
	return strings.TrimSpace(strings.ToLower(namespace)) == NamespaceSystem
}

// IsSystemNamespace reports whether the namespace is the reserved system namespace.
func IsSystemNamespace(namespace string) bool {
	return isSystemNamespace(namespace)
}

// validateAppNamespace checks that a computed namespace is safe for app workloads.
// Returns an error if the namespace is releasea-system or otherwise invalid.
func validateAppNamespace(namespace string) error {
	if isSystemNamespace(namespace) {
		return fmt.Errorf("namespace %q is reserved for platform components; application workloads cannot be deployed there", namespace)
	}
	if !isValidAppNamespace(namespace) {
		return fmt.Errorf("namespace %q is not a valid application namespace; allowed: %s, %s, %s",
			namespace, NamespaceProduction, NamespaceStaging, NamespaceDevelopment)
	}
	return nil
}

// ValidateAppNamespace validates that a namespace is safe for app workloads.
func ValidateAppNamespace(namespace string) error {
	return validateAppNamespace(namespace)
}
