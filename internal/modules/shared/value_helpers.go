package shared

import commonvalues "releaseaworker/internal/modules/shared/values"

func MapValue(value interface{}) map[string]interface{} {
	return commonvalues.MapValue(value)
}

func StringValue(source map[string]interface{}, key string) string {
	return commonvalues.StringValue(source, key)
}
