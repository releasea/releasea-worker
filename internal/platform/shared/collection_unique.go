package shared

import "strings"

func UniqueStrings(values []string) []string {
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

func HasHostSuffix(hosts []string, suffix string) bool {
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
