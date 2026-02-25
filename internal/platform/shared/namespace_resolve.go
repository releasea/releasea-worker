package shared

func ResolveNamespace(_ any, environment string) string {
	return ResolveAppNamespace(environment)
}
