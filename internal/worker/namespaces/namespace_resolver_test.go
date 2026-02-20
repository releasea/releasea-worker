package namespaces

import (
	"os"
	"testing"
)

func TestResolveAppNamespace_CoreEnvironments(t *testing.T) {
	cases := []struct {
		env  string
		want string
	}{
		{"prod", NamespaceProduction},
		{"production", NamespaceProduction},
		{"live", NamespaceProduction},
		{"staging", NamespaceStaging},
		{"stage", NamespaceStaging},
		{"uat", NamespaceStaging},
		{"pre-prod", NamespaceStaging},
		{"dev", NamespaceDevelopment},
		{"development", NamespaceDevelopment},
		{"qa", NamespaceDevelopment},
		{"sandbox", NamespaceDevelopment},
		{"test", NamespaceDevelopment},
		{"preview", NamespaceDevelopment},
	}
	for _, tc := range cases {
		t.Run(tc.env, func(t *testing.T) {
			got := resolveAppNamespace(tc.env)
			if got != tc.want {
				t.Errorf("resolveAppNamespace(%q) = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}

func TestResolveAppNamespace_EmptyDefaultsToProd(t *testing.T) {
	got := resolveAppNamespace("")
	if got != NamespaceProduction {
		t.Errorf("resolveAppNamespace(\"\") = %q, want %q", got, NamespaceProduction)
	}
}

func TestResolveAppNamespace_UnknownDefaultsToDev(t *testing.T) {
	cases := []string{"custom-env", "experiment", "demo", "perf"}
	for _, env := range cases {
		got := resolveAppNamespace(env)
		if got != NamespaceDevelopment {
			t.Errorf("resolveAppNamespace(%q) = %q, want %q", env, got, NamespaceDevelopment)
		}
	}
}

func TestResolveAppNamespace_NeverReturnsSystemNamespace(t *testing.T) {
	envs := []string{"prod", "staging", "dev", "qa", "", "releasea-system", "system"}
	for _, env := range envs {
		got := resolveAppNamespace(env)
		if got == NamespaceSystem {
			t.Errorf("resolveAppNamespace(%q) returned releasea-system, which is forbidden", env)
		}
	}
}

func TestValidateAppNamespace_BlocksSystemNamespace(t *testing.T) {
	if err := validateAppNamespace(NamespaceSystem); err == nil {
		t.Error("validateAppNamespace(releasea-system) should return an error")
	}
}

func TestValidateAppNamespace_AllowsFixedNamespaces(t *testing.T) {
	for _, ns := range []string{NamespaceProduction, NamespaceStaging, NamespaceDevelopment} {
		if err := validateAppNamespace(ns); err != nil {
			t.Errorf("validateAppNamespace(%q) returned error: %v", ns, err)
		}
	}
}

func TestResolveAppNamespace_CustomMapping(t *testing.T) {
	os.Setenv("RELEASEA_NAMESPACE_MAPPING", `{"perf":"releasea-apps-staging"}`)
	defer os.Unsetenv("RELEASEA_NAMESPACE_MAPPING")

	if got := resolveAppNamespace("perf"); got != NamespaceStaging {
		t.Errorf("resolveAppNamespace(perf) = %q, want %q", got, NamespaceStaging)
	}
}
