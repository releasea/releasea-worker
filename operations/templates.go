package operations

import (
	"strconv"
	"strings"
)

func renderTemplateResource(resource map[string]interface{}, replacements map[string]string) map[string]interface{} {
	if resource == nil {
		return map[string]interface{}{}
	}
	rendered := renderValue(resource, replacements)
	if out, ok := rendered.(map[string]interface{}); ok {
		return out
	}
	return map[string]interface{}{}
}

func renderValue(value interface{}, replacements map[string]string) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for key, val := range v {
			out[key] = renderValue(val, replacements)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(v))
		for i, item := range v {
			out[i] = renderValue(item, replacements)
		}
		return out
	case string:
		result := v
		for key, replacement := range replacements {
			result = strings.ReplaceAll(result, "{{"+key+"}}", replacement)
		}
		return result
	default:
		return value
	}
}

func normalizeResourceNumbers(value interface{}) map[string]interface{} {
	root, ok := value.(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	normalizeNumbers(root)
	return root
}

func normalizeNumbers(value interface{}) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, item := range v {
			if shouldCoerceNumeric(key) {
				if str, ok := item.(string); ok {
					cleaned := strings.TrimSpace(str)
					if parsed, err := strconv.Atoi(cleaned); err == nil {
						v[key] = parsed
						continue
					}
					if parsed, err := strconv.ParseFloat(cleaned, 64); err == nil {
						v[key] = int(parsed)
						continue
					}
					if digits := leadingDigits(cleaned); digits != "" {
						if parsed, err := strconv.Atoi(digits); err == nil {
							v[key] = parsed
							continue
						}
					}
				}
			}
			normalizeNumbers(item)
		}
	case []interface{}:
		for _, item := range v {
			normalizeNumbers(item)
		}
	}
}

func leadingDigits(value string) string {
	start := -1
	for i, r := range value {
		if r >= '0' && r <= '9' {
			if start == -1 {
				start = i
			}
			continue
		}
		if start >= 0 {
			return value[start:i]
		}
	}
	if start >= 0 {
		return value[start:]
	}
	return ""
}

func shouldCoerceNumeric(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "port", "replicas", "targetport", "backofflimit", "activedeadlineseconds":
		return true
	default:
		return false
	}
}
