package deploy

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"releaseaworker/internal/models"
	"strings"
	"testing"
)

type rewriteRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rewriteRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func withRewrittenDefaultTransport(t *testing.T, server *httptest.Server, fn func()) {
	t.Helper()
	target, _ := url.Parse(server.URL)
	base := server.Client().Transport
	original := http.DefaultTransport
	http.DefaultTransport = rewriteRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		clone.URL.Scheme = target.Scheme
		clone.URL.Host = target.Host
		return base.RoundTrip(clone)
	})
	t.Cleanup(func() {
		http.DefaultTransport = original
	})
	fn()
}

func generateRSAKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal rsa key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8}))
}

func TestSecretRefHelpers(t *testing.T) {
	if !isSecretRef("vault://path#key") {
		t.Fatalf("expected vault secret ref")
	}
	if !isSecretRef("secret://path#key") {
		t.Fatalf("expected generic secret ref")
	}
	if isSecretRef("plain-value") {
		t.Fatalf("did not expect plain value as secret ref")
	}

	name, key := splitSecretRef("path/to/secret#token")
	if name != "path/to/secret" || key != "token" {
		t.Fatalf("unexpected split secret ref %q %q", name, key)
	}
	name, key = splitSecretRef("path/to/secret")
	if name != "path/to/secret" || key != "" {
		t.Fatalf("unexpected split without key %q %q", name, key)
	}
}

func TestExtractSecretValue(t *testing.T) {
	data := map[string]interface{}{"value": "abc"}
	value, err := extractSecretValue(data, "")
	if err != nil || value != "abc" {
		t.Fatalf("expected default value extraction, got value=%q err=%v", value, err)
	}

	value, err = extractSecretValue(map[string]interface{}{"password": "secret"}, "password")
	if err != nil || value != "secret" {
		t.Fatalf("expected keyed value extraction, got value=%q err=%v", value, err)
	}

	if _, err := extractSecretValue(map[string]interface{}{"a": "b", "c": "d"}, ""); err == nil {
		t.Fatalf("expected key required error for multi-key payload")
	}
	if _, err := extractSecretValue(map[string]interface{}{"a": "b"}, "missing"); err == nil {
		t.Fatalf("expected missing key error")
	}
}

func TestBuildSecretResourceAndInjectEnvVars(t *testing.T) {
	secretResource := buildSecretResource("api", "apps", map[string]string{"API_KEY": "secret"})
	if sharedName := strings.TrimSpace(secretResource["kind"].(string)); sharedName != "Secret" {
		t.Fatalf("expected secret kind, got %q", sharedName)
	}

	resource := map[string]interface{}{
		"kind": "Deployment",
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "api"},
					},
				},
			},
		},
	}
	err := injectEnvVars(resource, map[string]string{"MODE": "prod"}, map[string]string{"API_KEY": "secret"}, "api")
	if err != nil {
		t.Fatalf("unexpected inject env vars error: %v", err)
	}
}

func TestResolveSecretValueValidationErrors(t *testing.T) {
	_, err := resolveSecretValue(context.Background(), models.Config{}, models.DeployContext{}, "prod", "secret://path#k")
	if err == nil {
		t.Fatalf("expected provider missing error")
	}

	ctxData := models.DeployContext{
		SecretProvider: &models.SecretProvider{Type: "aws"},
	}
	_, err = resolveSecretValue(context.Background(), models.Config{}, ctxData, "prod", "vault://path#k")
	if err == nil {
		t.Fatalf("expected vault provider mismatch error")
	}

	_, err = resolveSecretValue(context.Background(), models.Config{}, ctxData, "prod", "unsupported://x")
	if err == nil {
		t.Fatalf("expected unsupported secret reference error")
	}
}

func TestResolveVaultSecretAndEnvVars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": map[string]interface{}{
				"data": map[string]interface{}{
					"password": "vault-secret",
				},
			},
		})
	}))
	defer server.Close()

	provider := &models.SecretProvider{
		Type: "vault",
		Config: map[string]interface{}{
			"address": server.URL,
			"token":   "vault-token",
		},
	}

	value, err := resolveVaultSecret(context.Background(), provider, "secret/path#password")
	if err != nil || value != "vault-secret" {
		t.Fatalf("expected vault secret value, got value=%q err=%v", value, err)
	}

	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			Environment: map[string]string{
				"MODE":      "prod",
				"DB_SECRET": "secret://secret/path#password",
			},
		},
		SecretProvider: provider,
	}
	plain, secret, err := resolveEnvVars(context.Background(), models.Config{}, ctxData, "prod")
	if err != nil {
		t.Fatalf("unexpected env var resolution error: %v", err)
	}
	if plain["MODE"] != "prod" || secret["DB_SECRET"] != "vault-secret" {
		t.Fatalf("unexpected env resolution plain=%v secret=%v", plain, secret)
	}
}

func TestResolveAwsSecret(t *testing.T) {
	provider := &models.SecretProvider{Type: "aws", Config: map[string]interface{}{}}
	if _, err := resolveAwsSecret(context.Background(), provider, "mysecret#password"); err == nil {
		t.Fatalf("expected aws credentials validation error")
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"SecretString": `{"password":"aws-secret"}`,
		})
	}))
	defer server.Close()

	withRewrittenDefaultTransport(t, server, func() {
		provider := &models.SecretProvider{
			Type: "aws",
			Config: map[string]interface{}{
				"accessKeyId":     "AKIAEXAMPLE",
				"secretAccessKey": "secret",
				"region":          "us-east-1",
			},
		}
		value, err := resolveAwsSecret(context.Background(), provider, "mysecret#password")
		if err != nil || value != "aws-secret" {
			t.Fatalf("expected aws secret value, got value=%q err=%v", value, err)
		}
	})
}

func TestResolveGcpSecretAndToken(t *testing.T) {
	privateKeyPEM := generateRSAKeyPEM(t)
	tokenURIPath := "/oauth/token"
	secretData := base64.StdEncoding.EncodeToString([]byte("gcp-secret"))

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == tokenURIPath:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "gcp-access-token",
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/v1/projects/project-1/secrets/secret-a/versions/latest:access"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payload": map[string]interface{}{
					"data": secretData,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	serviceAccountJSONBytes, _ := json.Marshal(map[string]interface{}{
		"client_email": "worker@releasea.iam.gserviceaccount.com",
		"private_key":  privateKeyPEM,
		"token_uri":    server.URL + tokenURIPath,
	})
	serviceAccountJSON := string(serviceAccountJSONBytes)

	withRewrittenDefaultTransport(t, server, func() {
		token, err := fetchGcpAccessToken(context.Background(), serviceAccountJSON)
		if err != nil || token != "gcp-access-token" {
			t.Fatalf("expected gcp access token, got token=%q err=%v", token, err)
		}

		provider := &models.SecretProvider{
			Type: "gcp",
			Config: map[string]interface{}{
				"serviceAccountJson": serviceAccountJSON,
				"projectId":          "project-1",
			},
		}
		value, err := resolveGcpSecret(context.Background(), provider, "secret-a")
		if err != nil || value != "gcp-secret" {
			t.Fatalf("expected gcp secret value, got value=%q err=%v", value, err)
		}
	})
}

func TestGcpTokenAndSecretValidationErrors(t *testing.T) {
	if _, err := fetchGcpAccessToken(context.Background(), ""); err == nil {
		t.Fatalf("expected service account missing error")
	}
	if _, err := fetchGcpAccessToken(context.Background(), "{invalid}"); err == nil {
		t.Fatalf("expected invalid json error")
	}

	provider := &models.SecretProvider{
		Type: "gcp",
		Config: map[string]interface{}{
			"serviceAccountJson": "{}",
		},
	}
	if _, err := resolveGcpSecret(context.Background(), provider, ""); err == nil {
		t.Fatalf("expected missing secret name error")
	}
}

func TestSigningHelpers(t *testing.T) {
	privateKeyPEM := generateRSAKeyPEM(t)
	jwt, err := signJwt(map[string]interface{}{"sub": "worker"}, privateKeyPEM)
	if err != nil || strings.Count(jwt, ".") != 2 {
		t.Fatalf("expected signed jwt, got jwt=%q err=%v", jwt, err)
	}
	if _, err := signJwt(map[string]interface{}{"sub": "worker"}, "invalid"); err == nil {
		t.Fatalf("expected invalid private key error")
	}

	req, _ := http.NewRequest(http.MethodPost, "https://secretsmanager.us-east-1.amazonaws.com/", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")
	signedHeaders, authHeader, err := signAwsRequest(req, []byte("{}"), "AKIA", "secret", "us-east-1", "secretsmanager", "20250101T000000Z", "20250101")
	if err != nil {
		t.Fatalf("unexpected aws signing error: %v", err)
	}
	if signedHeaders == "" || authHeader == "" || !strings.Contains(authHeader, "Credential=AKIA") {
		t.Fatalf("unexpected aws signing output headers=%q auth=%q", signedHeaders, authHeader)
	}

	if hashSHA256([]byte("abc")) == "" {
		t.Fatalf("expected sha256 hash output")
	}
	if len(deriveAwsSigningKey("secret", "20250101", "us-east-1", "secretsmanager")) == 0 {
		t.Fatalf("expected derived signing key")
	}
	if len(hmacSHA256([]byte("k"), "v")) == 0 {
		t.Fatalf("expected hmac output")
	}
}

func TestResolveSecretValueProviderValidationBranches(t *testing.T) {
	awsProvider := models.DeployContext{SecretProvider: &models.SecretProvider{Type: "vault"}}
	if _, err := resolveSecretValue(context.Background(), models.Config{}, awsProvider, "prod", "aws://secret#key"); err == nil {
		t.Fatalf("expected aws provider mismatch error")
	}

	gcpProvider := models.DeployContext{SecretProvider: &models.SecretProvider{Type: "aws"}}
	if _, err := resolveSecretValue(context.Background(), models.Config{}, gcpProvider, "prod", "gcp://project/secret#key"); err == nil {
		t.Fatalf("expected gcp provider mismatch error")
	}
}

func TestResolveVaultSecretFailureBranches(t *testing.T) {
	if _, err := resolveVaultSecret(context.Background(), &models.SecretProvider{
		Type:   "vault",
		Config: map[string]interface{}{},
	}, "path#key"); err == nil {
		t.Fatalf("expected vault configuration validation error")
	}

	provider := &models.SecretProvider{
		Type: "vault",
		Config: map[string]interface{}{
			"address": "http://127.0.0.1:1",
			"token":   "token",
		},
	}
	if _, err := resolveVaultSecret(context.Background(), provider, "#key"); err == nil {
		t.Fatalf("expected missing vault secret path error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider.Config["address"] = server.URL
	if _, err := resolveVaultSecret(context.Background(), provider, "secret/path#key"); err == nil {
		t.Fatalf("expected vault request status error")
	}
}

func TestResolveAwsSecretFailureBranches(t *testing.T) {
	provider := &models.SecretProvider{
		Type: "aws",
		Config: map[string]interface{}{
			"accessKeyId":     "AKIAEXAMPLE",
			"secretAccessKey": "secret",
			"region":          "us-east-1",
		},
	}

	if _, err := resolveAwsSecret(context.Background(), provider, "#password"); err == nil {
		t.Fatalf("expected aws secret name missing error")
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	withRewrittenDefaultTransport(t, server, func() {
		if _, err := resolveAwsSecret(context.Background(), provider, "mysecret#password"); err == nil {
			t.Fatalf("expected aws request error for status >= 400")
		}
	})
}

func TestResolveGcpSecretFailureBranches(t *testing.T) {
	privateKeyPEM := generateRSAKeyPEM(t)

	t.Run("project missing", func(t *testing.T) {
		provider := &models.SecretProvider{
			Type: "gcp",
			Config: map[string]interface{}{
				"serviceAccountJson": "{}",
			},
		}
		if _, err := resolveGcpSecret(context.Background(), provider, "secret-a"); err == nil {
			t.Fatalf("expected project id missing error")
		}
	})

	t.Run("secret payload empty", func(t *testing.T) {
		tokenURIPath := "/oauth/token"
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == tokenURIPath:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"access_token": "token",
				})
			case r.Method == http.MethodGet:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"payload": map[string]interface{}{},
				})
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		serviceAccountJSONBytes, _ := json.Marshal(map[string]interface{}{
			"client_email": "worker@releasea.iam.gserviceaccount.com",
			"private_key":  privateKeyPEM,
			"token_uri":    server.URL + tokenURIPath,
		})
		provider := &models.SecretProvider{
			Type: "gcp",
			Config: map[string]interface{}{
				"serviceAccountJson": string(serviceAccountJSONBytes),
				"projectId":          "project-1",
			},
		}

		withRewrittenDefaultTransport(t, server, func() {
			if _, err := resolveGcpSecret(context.Background(), provider, "secret-a"); err == nil {
				t.Fatalf("expected gcp payload empty error")
			}
		})
	})
}

func TestResolveEnvVarsAndInjectEnvVarNoops(t *testing.T) {
	ctxData := models.DeployContext{
		Service: models.ServiceConfig{
			Environment: map[string]string{
				"":      "value",
				"EMPTY": "",
				"MODE":  "prod",
			},
		},
	}
	plain, secret, err := resolveEnvVars(context.Background(), models.Config{}, ctxData, "prod")
	if err != nil {
		t.Fatalf("unexpected resolveEnvVars error: %v", err)
	}
	if plain["MODE"] != "prod" || len(secret) != 0 {
		t.Fatalf("unexpected env var resolution plain=%v secret=%v", plain, secret)
	}

	if got := buildSecretResource("api", "", map[string]string{"A": "b"}); len(got) != 0 {
		t.Fatalf("expected empty secret resource when namespace is missing, got %v", got)
	}

	nonDeployment := map[string]interface{}{"kind": "Service"}
	if err := injectEnvVars(nonDeployment, map[string]string{"MODE": "prod"}, nil, "api"); err != nil {
		t.Fatalf("unexpected injectEnvVars noop error: %v", err)
	}

	emptyContainer := map[string]interface{}{
		"kind": "Deployment",
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{"invalid"},
				},
			},
		},
	}
	if err := injectEnvVars(emptyContainer, map[string]string{"": "skip"}, map[string]string{"": "skip"}, "api"); err != nil {
		t.Fatalf("unexpected injectEnvVars empty container error: %v", err)
	}
}
