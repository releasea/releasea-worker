package shared

import "strings"

func NormalizeType(deployTemplateID, strategyType string) string {
	if strings.EqualFold(deployTemplateID, "tpl-cronjob") {
		return "rolling"
	}
	switch strings.ToLower(strings.TrimSpace(strategyType)) {
	case "canary", "blue-green", "rolling":
		return strings.ToLower(strings.TrimSpace(strategyType))
	default:
		return "rolling"
	}
}

func NormalizeCanaryPercent(value int) int {
	if value <= 0 {
		value = 10
	}
	if value > 50 {
		return 50
	}
	return value
}

func ResolveBlueGreenSlots(primary string) (string, string) {
	if strings.EqualFold(strings.TrimSpace(primary), "green") {
		return "green", "blue"
	}
	return "blue", "green"
}
