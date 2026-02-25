package operations

import "testing"

func TestPayloadString(t *testing.T) {
	payload := map[string]interface{}{
		"name": " api ",
	}
	if got := PayloadString(payload, "name"); got != " api " {
		t.Fatalf("expected raw string value, got %q", got)
	}
	if got := PayloadString(payload, "missing"); got != "" {
		t.Fatalf("expected empty for missing key, got %q", got)
	}
}

func TestPayloadInt(t *testing.T) {
	payload := map[string]interface{}{
		"i":      5,
		"i32":    int32(6),
		"i64":    int64(7),
		"f64":    float64(8.9),
		"string": " 12 ",
		"bad":    "abc",
	}
	if got := PayloadInt(payload, "i"); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
	if got := PayloadInt(payload, "i32"); got != 6 {
		t.Fatalf("expected 6, got %d", got)
	}
	if got := PayloadInt(payload, "i64"); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
	if got := PayloadInt(payload, "f64"); got != 8 {
		t.Fatalf("expected 8, got %d", got)
	}
	if got := PayloadInt(payload, "string"); got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
	if got := PayloadInt(payload, "bad"); got != 0 {
		t.Fatalf("expected fallback 0, got %d", got)
	}
}

func TestPayloadResources(t *testing.T) {
	resources, err := PayloadResources(nil)
	if err != nil || resources != nil {
		t.Fatalf("expected nil resources for nil payload")
	}

	payload := map[string]interface{}{
		"resources": []interface{}{
			map[string]interface{}{"kind": "Deployment"},
		},
	}
	resources, err = PayloadResources(payload)
	if err != nil {
		t.Fatalf("unexpected error for valid resources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected one resource, got %d", len(resources))
	}

	payload["resources"] = []map[string]interface{}{
		{"kind": "Service"},
	}
	resources, err = PayloadResources(payload)
	if err != nil || len(resources) != 1 {
		t.Fatalf("expected one typed resource, got len=%d err=%v", len(resources), err)
	}

	payload["resources"] = []interface{}{"invalid"}
	if _, err := PayloadResources(payload); err == nil {
		t.Fatalf("expected error for invalid resources payload")
	}
}

func TestPayloadResourcesYAML(t *testing.T) {
	payload := map[string]interface{}{
		"resourcesYaml": "kind: Service",
	}
	if got := PayloadResourcesYAML(payload); got != "kind: Service" {
		t.Fatalf("unexpected yaml string: %q", got)
	}

	payload["resourcesYaml"] = []byte("kind: Deployment")
	if got := PayloadResourcesYAML(payload); got != "kind: Deployment" {
		t.Fatalf("unexpected yaml bytes conversion: %q", got)
	}

	payload["resourcesYaml"] = 123
	if got := PayloadResourcesYAML(payload); got != "123" {
		t.Fatalf("unexpected yaml fmt conversion: %q", got)
	}
}
