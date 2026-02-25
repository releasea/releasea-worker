package shared

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

type stringerValue struct {
	value string
}

func (s stringerValue) String() string {
	return s.value
}

func TestUniqueStrings(t *testing.T) {
	values := []string{" alpha ", "", "alpha", "beta", " beta ", "gamma"}
	got := UniqueStrings(values)
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestHasHostSuffix(t *testing.T) {
	hosts := []string{" api.releasea.local ", "web.releasea.example"}
	if !HasHostSuffix(hosts, ".releasea.local") {
		t.Fatalf("expected suffix match")
	}
	if HasHostSuffix(hosts, "") {
		t.Fatalf("did not expect empty suffix match")
	}
	if HasHostSuffix(hosts, ".internal") {
		t.Fatalf("did not expect .internal suffix match")
	}
}

func TestNormalizeType(t *testing.T) {
	if got := NormalizeType("tpl-cronjob", "canary"); got != "rolling" {
		t.Fatalf("expected rolling for cronjob, got %q", got)
	}
	if got := NormalizeType("", "CANARY"); got != "canary" {
		t.Fatalf("expected canary, got %q", got)
	}
	if got := NormalizeType("", "unknown"); got != "rolling" {
		t.Fatalf("expected rolling fallback, got %q", got)
	}
}

func TestNormalizeCanaryPercent(t *testing.T) {
	if got := NormalizeCanaryPercent(0); got != 10 {
		t.Fatalf("expected 10, got %d", got)
	}
	if got := NormalizeCanaryPercent(70); got != 50 {
		t.Fatalf("expected 50, got %d", got)
	}
	if got := NormalizeCanaryPercent(25); got != 25 {
		t.Fatalf("expected 25, got %d", got)
	}
}

func TestResolveBlueGreenSlots(t *testing.T) {
	primary, secondary := ResolveBlueGreenSlots("green")
	if primary != "green" || secondary != "blue" {
		t.Fatalf("expected green/blue, got %s/%s", primary, secondary)
	}
	primary, secondary = ResolveBlueGreenSlots("anything")
	if primary != "blue" || secondary != "green" {
		t.Fatalf("expected blue/green fallback, got %s/%s", primary, secondary)
	}
}

func TestEnvReaders(t *testing.T) {
	t.Setenv("SHARED_STRING", " value ")
	t.Setenv("SHARED_INT", " 123 ")
	t.Setenv("SHARED_BOOL_TRUE", "yes")
	t.Setenv("SHARED_BOOL_FALSE", "0")
	t.Setenv("SHARED_INT_BAD", "abc")

	if got := String("SHARED_STRING", "fallback"); got != "value" {
		t.Fatalf("expected value, got %q", got)
	}
	if got := String("SHARED_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
	if got := Int("SHARED_INT", 9); got != 123 {
		t.Fatalf("expected 123, got %d", got)
	}
	if got := Int("SHARED_INT_BAD", 9); got != 9 {
		t.Fatalf("expected fallback 9, got %d", got)
	}
	if got := EnvInt("SHARED_INT", 0); got != 123 {
		t.Fatalf("expected 123 from EnvInt, got %d", got)
	}
	if got := Bool("SHARED_BOOL_TRUE", false); !got {
		t.Fatalf("expected true bool parse")
	}
	if got := Bool("SHARED_BOOL_FALSE", true); got {
		t.Fatalf("expected false bool parse")
	}
	if got := Bool("SHARED_BOOL_UNKNOWN", true); !got {
		t.Fatalf("expected fallback true")
	}
}

func TestKubeNameHelpers(t *testing.T) {
	if got := ResolvePort(0); got != 80 {
		t.Fatalf("expected default port 80, got %d", got)
	}
	if got := ToKubeName(" My_App@@Name "); got != "my-app-name" {
		t.Fatalf("unexpected kube name: %q", got)
	}
	if got := NormalizeNamespace(""); got != "releasea-apps-prod" {
		t.Fatalf("expected default namespace, got %q", got)
	}
	longName := strings.Repeat("a", 70)
	if got := NormalizeNamespace(longName); len(got) > 63 {
		t.Fatalf("expected truncated namespace <=63, got len=%d", len(got))
	}
}

func TestNormalizeSourceType(t *testing.T) {
	if got := NormalizeSourceType("docker"); got != "registry" {
		t.Fatalf("expected registry, got %q", got)
	}
	if got := NormalizeSourceType("git"); got != "git" {
		t.Fatalf("expected git, got %q", got)
	}
	if got := NormalizeSourceType("other"); got != "" {
		t.Fatalf("expected empty fallback, got %q", got)
	}
}

func TestMapAndStringValue(t *testing.T) {
	m := map[string]interface{}{"name": " releasea ", "count": 5, "custom": stringerValue{value: " ok "}}
	if got := MapValue(m); got["name"] != " releasea " {
		t.Fatalf("expected map passthrough")
	}
	if got := MapValue(nil); len(got) != 0 {
		t.Fatalf("expected empty map for nil")
	}
	if got := StringValue(m, "name"); got != "releasea" {
		t.Fatalf("expected trimmed string, got %q", got)
	}
	if got := StringValue(m, "count"); got != "5" {
		t.Fatalf("expected sprint value 5, got %q", got)
	}
	if got := StringValue(m, "custom"); got != "ok" {
		t.Fatalf("expected stringer value, got %q", got)
	}
	if got := StringValue(nil, "name"); got != "" {
		t.Fatalf("expected empty for nil source, got %q", got)
	}
}

func TestRenderTemplateResource(t *testing.T) {
	input := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "{{service}}",
		},
		"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{
					"image": "{{image}}",
				},
			},
		},
	}
	rendered := RenderTemplateResource(input, map[string]string{
		"service": "api",
		"image":   "repo/api:v1",
	})
	meta := MapValue(rendered["metadata"])
	if name := StringValue(meta, "name"); name != "api" {
		t.Fatalf("expected rendered name api, got %q", name)
	}
}

func TestNormalizeResourceNumbers(t *testing.T) {
	resource := map[string]interface{}{
		"kind": "Deployment",
		"spec": map[string]interface{}{
			"replicas": "3",
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"ports": []interface{}{
								map[string]interface{}{
									"containerPort": "8080",
									"targetPort":    "9090",
								},
							},
						},
					},
				},
			},
			"backoffLimit": "12seconds",
		},
	}
	normalized := NormalizeResourceNumbers(resource)
	spec := MapValue(normalized["spec"])
	if _, ok := spec["replicas"].(int); !ok {
		t.Fatalf("expected replicas coerced to int")
	}
	if _, ok := spec["backoffLimit"].(int); !ok {
		t.Fatalf("expected backoffLimit coerced to int")
	}
}

func TestNamespaceResolutionAndValidation(t *testing.T) {
	t.Setenv("RELEASEA_NAMESPACE_MAPPING", "")
	if got := ResolveAppNamespace(""); got != NamespaceProduction {
		t.Fatalf("expected default prod namespace, got %q", got)
	}
	if got := ResolveAppNamespace("unknown-env"); got != NamespaceDevelopment {
		t.Fatalf("expected development fallback, got %q", got)
	}
	if err := ValidateAppNamespace(NamespaceProduction); err != nil {
		t.Fatalf("expected production namespace valid, got %v", err)
	}
	if err := ValidateAppNamespace(NamespaceSystem); err == nil {
		t.Fatalf("expected system namespace rejection")
	}
	if err := ValidateAppNamespace("invalid"); err == nil {
		t.Fatalf("expected invalid namespace rejection")
	}
}

func TestNamespaceMappingOverride(t *testing.T) {
	t.Setenv("RELEASEA_NAMESPACE_MAPPING", fmt.Sprintf(`{"qa2":"%s","hack":"kube-system"}`, NamespaceStaging))
	if got := ResolveAppNamespace("qa2"); got != NamespaceStaging {
		t.Fatalf("expected override namespace, got %q", got)
	}
	// Invalid override value must be ignored and fallback to development.
	if got := ResolveAppNamespace("hack"); got != NamespaceDevelopment {
		t.Fatalf("expected fallback development for invalid override, got %q", got)
	}
	if got := ResolveNamespace(nil, "qa2"); got != NamespaceStaging {
		t.Fatalf("expected ResolveNamespace wrapper to use override, got %q", got)
	}
}
