package shared

import "strings"

func ResolvePort(value int) int {
	if value <= 0 {
		return 80
	}
	return value
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
