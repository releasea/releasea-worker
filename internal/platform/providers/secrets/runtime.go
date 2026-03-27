package secretsproviders

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"releaseaworker/internal/platform/models"
	"releaseaworker/internal/platform/shared"
	"strings"
	"time"
)

type Runtime interface {
	ID() string
	Resolve(ctx context.Context, provider *models.SecretProvider, ref string) (string, error)
}

func IsSecretRef(value string) bool {
	return strings.HasPrefix(value, "vault://") ||
		strings.HasPrefix(value, "aws://") ||
		strings.HasPrefix(value, "gcp://") ||
		strings.HasPrefix(value, "secret://")
}

func ResolveReference(ctx context.Context, provider *models.SecretProvider, environment, value string) (string, error) {
	value = strings.ReplaceAll(value, "{{env}}", environment)
	if strings.HasPrefix(value, "secret://") {
		if provider == nil {
			return "", errors.New("secret provider not configured")
		}
		value = strings.Replace(value, "secret://", provider.Type+"://", 1)
	}
	switch {
	case strings.HasPrefix(value, "vault://"):
		if provider == nil || provider.Type != "vault" {
			return "", errors.New("vault provider not configured")
		}
		runtime, _ := ResolveRuntime(provider.Type)
		return runtime.Resolve(ctx, provider, strings.TrimPrefix(value, "vault://"))
	case strings.HasPrefix(value, "aws://"):
		if provider == nil || provider.Type != "aws" {
			return "", errors.New("aws provider not configured")
		}
		runtime, _ := ResolveRuntime(provider.Type)
		return runtime.Resolve(ctx, provider, strings.TrimPrefix(value, "aws://"))
	case strings.HasPrefix(value, "gcp://"):
		if provider == nil || provider.Type != "gcp" {
			return "", errors.New("gcp provider not configured")
		}
		runtime, _ := ResolveRuntime(provider.Type)
		return runtime.Resolve(ctx, provider, strings.TrimPrefix(value, "gcp://"))
	default:
		return "", errors.New("unsupported secret reference")
	}
}

func ResolveRuntime(providerType string) (Runtime, bool) {
	switch strings.ToLower(strings.TrimSpace(providerType)) {
	case "vault":
		return vaultRuntime{}, true
	case "aws":
		return awsRuntime{}, true
	case "gcp":
		return gcpRuntime{}, true
	default:
		return nil, false
	}
}

type vaultRuntime struct{}

func (vaultRuntime) ID() string { return "vault" }

func (vaultRuntime) Resolve(ctx context.Context, provider *models.SecretProvider, ref string) (string, error) {
	address := strings.TrimSpace(shared.StringValue(provider.Config, "address"))
	token := strings.TrimSpace(shared.StringValue(provider.Config, "token"))
	if address == "" || token == "" {
		return "", errors.New("vault address/token missing")
	}
	path, key := splitSecretRef(ref)
	if path == "" {
		return "", errors.New("vault secret path missing")
	}
	endpoint := strings.TrimRight(address, "/") + "/v1/" + strings.TrimLeft(path, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Vault-Token", token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("vault request failed: %s", resp.Status)
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	data := shared.MapValue(payload["data"])
	if nested := shared.MapValue(data["data"]); len(nested) > 0 {
		data = nested
	}
	return extractSecretValue(data, key)
}

type awsRuntime struct{}

func (awsRuntime) ID() string { return "aws" }

func (awsRuntime) Resolve(ctx context.Context, provider *models.SecretProvider, ref string) (string, error) {
	accessKey := shared.StringValue(provider.Config, "accessKeyId")
	secretKey := shared.StringValue(provider.Config, "secretAccessKey")
	region := shared.StringValue(provider.Config, "region")
	if region == "" {
		region = "us-east-1"
	}
	if accessKey == "" || secretKey == "" {
		return "", errors.New("aws credentials missing")
	}
	secretName, jsonKey := splitSecretRef(ref)
	if secretName == "" {
		return "", errors.New("aws secret name missing")
	}
	endpoint := fmt.Sprintf("https://secretsmanager.%s.amazonaws.com/", region)
	body := map[string]string{"SecretId": secretName}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "secretsmanager.GetSecretValue")

	amzDate := time.Now().UTC().Format("20060102T150405Z")
	dateStamp := time.Now().UTC().Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)

	signedHeaders, authHeader, err := signAwsRequest(req, payload, accessKey, secretKey, region, "secretsmanager", amzDate, dateStamp)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-Amz-Content-Sha256", hashSHA256(payload))
	req.Header.Set("SignedHeaders", signedHeaders)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("aws secret request failed: %s", resp.Status)
	}
	var response struct {
		SecretString string `json:"SecretString"`
		SecretBinary string `json:"SecretBinary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	secretValue := response.SecretString
	if secretValue == "" && response.SecretBinary != "" {
		decoded, err := base64.StdEncoding.DecodeString(response.SecretBinary)
		if err != nil {
			return "", err
		}
		secretValue = string(decoded)
	}
	if jsonKey == "" {
		return secretValue, nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(secretValue), &data); err != nil {
		return "", err
	}
	return extractSecretValue(data, jsonKey)
}

type gcpRuntime struct{}

func (gcpRuntime) ID() string { return "gcp" }

func (gcpRuntime) Resolve(ctx context.Context, provider *models.SecretProvider, ref string) (string, error) {
	serviceAccount := shared.StringValue(provider.Config, "serviceAccountJson")
	projectID := shared.StringValue(provider.Config, "projectId")
	secretRef, version := splitSecretRef(ref)
	if secretRef == "" {
		return "", errors.New("gcp secret name missing")
	}
	if strings.Contains(secretRef, "/") {
		parts := strings.SplitN(secretRef, "/", 2)
		projectID = parts[0]
		secretRef = parts[1]
	}
	if projectID == "" {
		return "", errors.New("gcp project id missing")
	}
	if version == "" {
		version = "latest"
	}
	token, err := fetchGcpAccessToken(ctx, serviceAccount)
	if err != nil {
		return "", err
	}
	endpoint := fmt.Sprintf("https://secretmanager.googleapis.com/v1/projects/%s/secrets/%s/versions/%s:access", projectID, secretRef, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gcp secret request failed: %s", resp.Status)
	}
	var response struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	if response.Payload.Data == "" {
		return "", errors.New("gcp secret payload empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(response.Payload.Data)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func fetchGcpAccessToken(ctx context.Context, serviceAccountJSON string) (string, error) {
	if serviceAccountJSON == "" {
		return "", errors.New("gcp service account json missing")
	}
	var sa struct {
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal([]byte(serviceAccountJSON), &sa); err != nil {
		return "", err
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return "", errors.New("gcp service account invalid")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	iat := time.Now().Unix()
	exp := iat + 3600
	claims := map[string]interface{}{
		"iss":   sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/cloud-platform",
		"aud":   sa.TokenURI,
		"iat":   iat,
		"exp":   exp,
	}
	jwt, err := signJwt(claims, sa.PrivateKey)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", jwt)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("gcp token request failed: %s", resp.Status)
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}
	if response.AccessToken == "" {
		return "", errors.New("gcp access token missing")
	}
	return response.AccessToken, nil
}

func signJwt(claims map[string]interface{}, privateKeyPEM string) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claimsJSON)
	unsigned := encodedHeader + "." + encodedClaims

	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return "", errors.New("invalid private key")
	}
	privKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		privKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", err
		}
	}
	rsaKey, ok := privKey.(*rsa.PrivateKey)
	if !ok {
		return "", errors.New("private key is not RSA")
	}
	hashed := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	encodedSig := base64.RawURLEncoding.EncodeToString(signature)
	return unsigned + "." + encodedSig, nil
}

func signAwsRequest(req *http.Request, payload []byte, accessKey, secretKey, region, service, amzDate, dateStamp string) (string, string, error) {
	canonicalURI := "/"
	canonicalQuery := ""
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-date:%s\nx-amz-target:%s\n",
		req.Header.Get("Content-Type"),
		req.URL.Host,
		amzDate,
		req.Header.Get("X-Amz-Target"),
	)
	signedHeaders := "content-type;host;x-amz-date;x-amz-target"
	payloadHash := hashSHA256(payload)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, region, service)
	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		credentialScope,
		hashSHA256([]byte(canonicalRequest)),
	}, "\n")

	signingKey := deriveAwsSigningKey(secretKey, dateStamp, region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	authorization := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, accessKey, credentialScope, signedHeaders, signature)

	return signedHeaders, authorization, nil
}

func deriveAwsSigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hashSHA256(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func splitSecretRef(ref string) (string, string) {
	parts := strings.SplitN(ref, "#", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return ref, ""
}

func extractSecretValue(data map[string]interface{}, key string) (string, error) {
	if key == "" {
		if len(data) == 1 {
			for _, value := range data {
				return fmt.Sprint(value), nil
			}
		}
		if value, ok := data["value"]; ok {
			return fmt.Sprint(value), nil
		}
		return "", errors.New("secret key required")
	}
	value, ok := data[key]
	if !ok {
		return "", errors.New("secret key not found")
	}
	return fmt.Sprint(value), nil
}

func FetchGcpAccessToken(ctx context.Context, serviceAccountJSON string) (string, error) {
	return fetchGcpAccessToken(ctx, serviceAccountJSON)
}

func SignJWT(claims map[string]interface{}, privateKeyPEM string) (string, error) {
	return signJwt(claims, privateKeyPEM)
}

func SignAWSRequest(req *http.Request, payload []byte, accessKey, secretKey, region, service, amzDate, dateStamp string) (string, string, error) {
	return signAwsRequest(req, payload, accessKey, secretKey, region, service, amzDate, dateStamp)
}

func HashSHA256(data []byte) string {
	return hashSHA256(data)
}

func SplitSecretRef(ref string) (string, string) {
	return splitSecretRef(ref)
}

func ExtractSecretValue(data map[string]interface{}, key string) (string, error) {
	return extractSecretValue(data, key)
}

func DeriveAwsSigningKey(secret, dateStamp, region, service string) []byte {
	return deriveAwsSigningKey(secret, dateStamp, region, service)
}

func HmacSHA256(key []byte, data string) []byte {
	return hmacSHA256(key, data)
}
