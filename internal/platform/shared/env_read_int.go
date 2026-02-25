package shared

func EnvInt(key string, fallback int) int {
	return Int(key, fallback)
}
