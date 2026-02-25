package models

import "testing"

func TestOperationContractCatalogVersion(t *testing.T) {
	if OperationContractCatalogVersion != "v1" {
		t.Fatalf("operation contract version = %q, want v1", OperationContractCatalogVersion)
	}
}

func TestSupportedOperationTypes(t *testing.T) {
	supported := SupportedOperationTypes()
	if len(supported) != 7 {
		t.Fatalf("supported operation types = %d, want 7", len(supported))
	}
	if supported[0] != OperationTypeServiceDeploy {
		t.Fatalf("first supported operation type = %q, want %q", supported[0], OperationTypeServiceDeploy)
	}
	if !IsSupportedOperationType(OperationTypeRuleDelete) {
		t.Fatalf("%s should be supported", OperationTypeRuleDelete)
	}
	if IsSupportedOperationType("unknown.operation") {
		t.Fatalf("unknown operation type should not be supported")
	}
}

func TestSupportedOperationStatuses(t *testing.T) {
	statuses := SupportedOperationStatuses()
	if len(statuses) != 4 {
		t.Fatalf("supported operation statuses = %d, want 4", len(statuses))
	}
	if statuses[0] != OperationStatusQueued {
		t.Fatalf("first operation status = %q, want %q", statuses[0], OperationStatusQueued)
	}

	statuses[0] = "mutated"
	again := SupportedOperationStatuses()
	if again[0] != OperationStatusQueued {
		t.Fatalf("SupportedOperationStatuses returned shared backing slice")
	}
}
