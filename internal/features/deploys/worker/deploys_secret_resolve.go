package deploy

import (
	"context"
	"fmt"
	"net/http"
	"releaseaworker/internal/platform/models"
	secretsproviders "releaseaworker/internal/platform/providers/secrets"
)

func resolveEnvVars(ctx context.Context, cfg models.Config, ctxData models.DeployContext, environment string) (map[string]string, map[string]string, error) {
	plain := map[string]string{}
	secret := map[string]string{}
	for key, value := range ctxData.Service.Environment {
		if key == "" || value == "" {
			continue
		}
		if isSecretRef(value) {
			resolved, err := resolveSecretValue(ctx, cfg, ctxData, environment, value)
			if err != nil {
				return nil, nil, err
			}
			secret[key] = resolved
			continue
		}
		plain[key] = value
	}
	return plain, secret, nil
}

func isSecretRef(value string) bool {
	return secretsproviders.IsSecretRef(value)
}

func resolveSecretValue(ctx context.Context, cfg models.Config, ctxData models.DeployContext, environment, value string) (string, error) {
	return secretsproviders.ResolveReference(ctx, ctxData.SecretProvider, environment, value)
}

func resolveVaultSecret(ctx context.Context, provider *models.SecretProvider, ref string) (string, error) {
	runtime, ok := secretsproviders.ResolveRuntime("vault")
	if !ok {
		return "", fmt.Errorf("vault runtime not available")
	}
	return runtime.Resolve(ctx, provider, ref)
}

func resolveAwsSecret(ctx context.Context, provider *models.SecretProvider, ref string) (string, error) {
	runtime, ok := secretsproviders.ResolveRuntime("aws")
	if !ok {
		return "", fmt.Errorf("aws runtime not available")
	}
	return runtime.Resolve(ctx, provider, ref)
}

func resolveGcpSecret(ctx context.Context, provider *models.SecretProvider, ref string) (string, error) {
	runtime, ok := secretsproviders.ResolveRuntime("gcp")
	if !ok {
		return "", fmt.Errorf("gcp runtime not available")
	}
	return runtime.Resolve(ctx, provider, ref)
}

func injectEnvVars(resource map[string]interface{}, plainEnv, secretEnv map[string]string, secretName string) error {
	if len(plainEnv) == 0 && len(secretEnv) == 0 {
		return nil
	}
	kind, _ := resource["kind"].(string)
	if kind != "Deployment" {
		return nil
	}
	spec, _ := resource["spec"].(map[string]interface{})
	template, _ := spec["template"].(map[string]interface{})
	templateSpec, _ := template["spec"].(map[string]interface{})
	containers, _ := templateSpec["containers"].([]interface{})
	if len(containers) == 0 {
		return nil
	}
	container, ok := containers[0].(map[string]interface{})
	if !ok {
		return nil
	}
	envList := make([]interface{}, 0, len(plainEnv)+len(secretEnv))
	for key, value := range plainEnv {
		if key == "" {
			continue
		}
		envList = append(envList, map[string]interface{}{
			"name":  key,
			"value": value,
		})
	}
	for key := range secretEnv {
		if key == "" {
			continue
		}
		envList = append(envList, map[string]interface{}{
			"name": key,
			"valueFrom": map[string]interface{}{
				"secretKeyRef": map[string]interface{}{
					"name": secretName + "-secrets",
					"key":  key,
				},
			},
		})
	}
	if len(envList) == 0 {
		return nil
	}
	container["env"] = envList
	containers[0] = container
	templateSpec["containers"] = containers
	template["spec"] = templateSpec
	spec["template"] = template
	resource["spec"] = spec
	return nil
}

func buildSecretResource(serviceName, namespace string, secrets map[string]string) map[string]interface{} {
	if namespace == "" {
		return map[string]interface{}{}
	}
	stringData := map[string]interface{}{}
	for key, value := range secrets {
		stringData[key] = value
	}
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]interface{}{
			"name":      serviceName + "-secrets",
			"namespace": namespace,
		},
		"type":       "Opaque",
		"stringData": stringData,
	}
}

func fetchGcpAccessToken(ctx context.Context, serviceAccountJSON string) (string, error) {
	return secretsproviders.FetchGcpAccessToken(ctx, serviceAccountJSON)
}

func signJwt(claims map[string]interface{}, privateKeyPEM string) (string, error) {
	return secretsproviders.SignJWT(claims, privateKeyPEM)
}

func signAwsRequest(req *http.Request, payload []byte, accessKey, secretKey, region, service, amzDate, dateStamp string) (string, string, error) {
	return secretsproviders.SignAWSRequest(req, payload, accessKey, secretKey, region, service, amzDate, dateStamp)
}

func hashSHA256(data []byte) string {
	return secretsproviders.HashSHA256(data)
}

func splitSecretRef(ref string) (string, string) {
	return secretsproviders.SplitSecretRef(ref)
}

func extractSecretValue(data map[string]interface{}, key string) (string, error) {
	return secretsproviders.ExtractSecretValue(data, key)
}

func deriveAwsSigningKey(secret, dateStamp, region, service string) []byte {
	return secretsproviders.DeriveAwsSigningKey(secret, dateStamp, region, service)
}

func hmacSHA256(key []byte, data string) []byte {
	return secretsproviders.HmacSHA256(key, data)
}
