package deploy

import (
	"testing"

	commonvalues "releaseaworker/internal/platform/shared"
)

func TestZeroDowntimeRollingStrategyKeepsExistingCapacity(t *testing.T) {
	resource := map[string]interface{}{"spec": map[string]interface{}{}}
	setZeroDowntimeRollingStrategy(resource)
	rolling := commonvalues.MapValue(commonvalues.MapValue(commonvalues.MapValue(resource["spec"])["strategy"])["rollingUpdate"])
	if rolling["maxUnavailable"] != 0 {
		t.Fatalf("expected no pods to be removed before replacements are ready, got %#v", rolling["maxUnavailable"])
	}
	if rolling["maxSurge"] != "25%" {
		t.Fatalf("expected spare capacity while the new pods start, got %#v", rolling["maxSurge"])
	}
}
