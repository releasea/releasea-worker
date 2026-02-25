package ops

import commonvalues "releaseaworker/common/values"

func mapValue(value interface{}) map[string]interface{} {
	return commonvalues.MapValue(value)
}

func stringValue(source map[string]interface{}, key string) string {
	return commonvalues.StringValue(source, key)
}
