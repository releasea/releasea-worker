package kubernetes

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"releaseaworker/internal/platform/shared"
	"strings"
	"time"
)

func KubeAPIBaseURL() string {
	base := strings.TrimSpace(os.Getenv("RELEASEA_KUBE_API_BASE_URL"))
	if base == "" {
		base = "https://kubernetes.default.svc"
	}
	return strings.TrimRight(base, "/")
}

func ApplyResource(ctx context.Context, client *http.Client, token string, resource map[string]interface{}) error {
	resource = shared.NormalizeResourceNumbers(resource)
	kind, _ := resource["kind"].(string)
	apiVersion, _ := resource["apiVersion"].(string)
	meta, _ := resource["metadata"].(map[string]interface{})
	name, _ := meta["name"].(string)
	namespace, _ := meta["namespace"].(string)
	if kind == "" || apiVersion == "" || name == "" || namespace == "" {
		return errors.New("resource missing kind/apiVersion/name/namespace")
	}
	resourceURL, listURL, err := ResourceURLs(apiVersion, kind, namespace, name)
	if err != nil {
		return err
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return err
	}
	getReq.Header.Set("Authorization", "Bearer "+token)
	getResp, err := client.Do(getReq)
	if err != nil {
		return err
	}
	defer getResp.Body.Close()
	if getResp.StatusCode == http.StatusNotFound {
		body, err := json.Marshal(resource)
		if err != nil {
			return err
		}
		createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, listURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		createReq.Header.Set("Authorization", "Bearer "+token)
		createReq.Header.Set("Content-Type", "application/json")
		createResp, err := client.Do(createReq)
		if err != nil {
			return err
		}
		defer createResp.Body.Close()
		if createResp.StatusCode >= 400 {
			details, _ := io.ReadAll(createResp.Body)
			msg := strings.TrimSpace(string(details))
			if msg != "" {
				return fmt.Errorf("create %s failed: %s: %s", kind, createResp.Status, msg)
			}
			return fmt.Errorf("create %s failed: %s", kind, createResp.Status)
		}
		return nil
	}
	if getResp.StatusCode >= 400 {
		details, _ := io.ReadAll(getResp.Body)
		msg := strings.TrimSpace(string(details))
		if msg != "" {
			return fmt.Errorf("fetch %s failed: %s: %s", kind, getResp.Status, msg)
		}
		return fmt.Errorf("fetch %s failed: %s", kind, getResp.Status)
	}
	var existing map[string]interface{}
	if err := json.NewDecoder(getResp.Body).Decode(&existing); err != nil {
		return err
	}
	if metaExisting, ok := existing["metadata"].(map[string]interface{}); ok {
		if rv, ok := metaExisting["resourceVersion"].(string); ok && rv != "" {
			meta["resourceVersion"] = rv
			resource["metadata"] = meta
		}
	}
	body, err := json.Marshal(resource)
	if err != nil {
		return err
	}
	updateReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resourceURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	updateReq.Header.Set("Authorization", "Bearer "+token)
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp, err := client.Do(updateReq)
	if err != nil {
		return err
	}
	defer updateResp.Body.Close()
	if updateResp.StatusCode >= 400 {
		details, _ := io.ReadAll(updateResp.Body)
		msg := strings.TrimSpace(string(details))
		if msg != "" {
			return fmt.Errorf("update %s failed: %s: %s", kind, updateResp.Status, msg)
		}
		return fmt.Errorf("update %s failed: %s", kind, updateResp.Status)
	}
	return nil
}

func DeleteResource(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) error {
	if kind == "" || apiVersion == "" || name == "" || namespace == "" {
		return errors.New("resource missing kind/apiVersion/name/namespace")
	}
	resourceURL, _, err := ResourceURLs(apiVersion, kind, namespace, name)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, resourceURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete %s failed: %s", kind, resp.Status)
	}
	return nil
}

func DeleteResourcesByLabel(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, selector string) error {
	if kind == "" || apiVersion == "" || namespace == "" || strings.TrimSpace(selector) == "" {
		return errors.New("resource missing kind/apiVersion/namespace/selector")
	}
	_, listURL, err := ResourceURLs(apiVersion, kind, namespace, "placeholder")
	if err != nil {
		return err
	}
	query := url.Values{}
	query.Set("labelSelector", selector)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, listURL+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("delete %s by selector failed: %s", kind, resp.Status)
	}
	return nil
}

func ResourceExists(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (bool, error) {
	if kind == "" || apiVersion == "" || name == "" || namespace == "" {
		return false, errors.New("resource missing kind/apiVersion/name/namespace")
	}
	resourceURL, _, err := ResourceURLs(apiVersion, kind, namespace, name)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("fetch %s failed: %s", kind, resp.Status)
	}
	return true, nil
}

func FetchResourceAsMap(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (map[string]interface{}, error) {
	if kind == "" || apiVersion == "" || name == "" || namespace == "" {
		return nil, errors.New("resource missing kind/apiVersion/name/namespace")
	}
	resourceURL, _, err := ResourceURLs(apiVersion, kind, namespace, name)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, resourceURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s %s not found in %s", kind, name, namespace)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("fetch %s %s failed: %s", kind, name, resp.Status)
	}
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func CleanResourceForReapply(resource map[string]interface{}) {
	delete(resource, "status")
	meta, ok := resource["metadata"].(map[string]interface{})
	if !ok {
		return
	}
	delete(meta, "uid")
	delete(meta, "creationTimestamp")
	delete(meta, "resourceVersion")
	delete(meta, "generation")
	delete(meta, "managedFields")
	annotations, ok := meta["annotations"].(map[string]interface{})
	if ok {
		delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
		delete(annotations, "deployment.kubernetes.io/revision")
		if len(annotations) == 0 {
			delete(meta, "annotations")
		}
	}

	kind := strings.ToLower(strings.TrimSpace(shared.StringValue(resource, "kind")))
	if kind == "service" {
		spec, ok := resource["spec"].(map[string]interface{})
		if ok {
			delete(spec, "clusterIP")
			delete(spec, "clusterIPs")
			delete(spec, "ipFamilies")
			delete(spec, "ipFamilyPolicy")
		}
	}
}

func ResourceURLs(apiVersion, kind, namespace, name string) (string, string, error) {
	baseURL := KubeAPIBaseURL()
	switch kind {
	case "Deployment":
		base := fmt.Sprintf("%s/apis/apps/v1/namespaces/%s/deployments", baseURL, namespace)
		return base + "/" + name, base, nil
	case "Service":
		base := fmt.Sprintf("%s/api/v1/namespaces/%s/services", baseURL, namespace)
		return base + "/" + name, base, nil
	case "ConfigMap":
		base := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps", baseURL, namespace)
		return base + "/" + name, base, nil
	case "Secret":
		base := fmt.Sprintf("%s/api/v1/namespaces/%s/secrets", baseURL, namespace)
		return base + "/" + name, base, nil
	case "CronJob":
		base := fmt.Sprintf("%s/apis/batch/v1/namespaces/%s/cronjobs", baseURL, namespace)
		return base + "/" + name, base, nil
	case "Job":
		base := fmt.Sprintf("%s/apis/batch/v1/namespaces/%s/jobs", baseURL, namespace)
		return base + "/" + name, base, nil
	case "HorizontalPodAutoscaler":
		base := fmt.Sprintf("%s/apis/autoscaling/v2/namespaces/%s/horizontalpodautoscalers", baseURL, namespace)
		return base + "/" + name, base, nil
	case "VirtualService":
		base := fmt.Sprintf("%s/apis/networking.istio.io/v1beta1/namespaces/%s/virtualservices", baseURL, namespace)
		return base + "/" + name, base, nil
	case "AuthorizationPolicy":
		base := fmt.Sprintf("%s/apis/security.istio.io/v1beta1/namespaces/%s/authorizationpolicies", baseURL, namespace)
		return base + "/" + name, base, nil
	default:
		return "", "", fmt.Errorf("unsupported kind %s (apiVersion %s)", kind, apiVersion)
	}
}

func KubeClient() (*http.Client, string, error) {
	tokenPath := strings.TrimSpace(os.Getenv("RELEASEA_KUBE_TOKEN_FILE"))
	if tokenPath == "" {
		tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	tokenBytes, err := os.ReadFile(filepath.Clean(tokenPath))
	if err != nil {
		return nil, "", err
	}

	insecureSkipVerify := strings.EqualFold(strings.TrimSpace(os.Getenv("RELEASEA_KUBE_INSECURE_SKIP_VERIFY")), "true")

	tlsConfig := &tls.Config{}
	if insecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	} else {
		caPath := strings.TrimSpace(os.Getenv("RELEASEA_KUBE_CA_FILE"))
		if caPath == "" {
			caPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
		}
		ca, err := os.ReadFile(filepath.Clean(caPath))
		if err != nil {
			return nil, "", err
		}
		pool := x509.NewCertPool()
		if ok := pool.AppendCertsFromPEM(ca); !ok {
			return nil, "", errors.New("failed to load cluster CA")
		}
		tlsConfig.RootCAs = pool
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	token := strings.TrimSpace(string(tokenBytes))
	return client, token, nil
}

func EnsureNamespace(ctx context.Context, client *http.Client, token, namespace string) error {
	if namespace == "" {
		return errors.New("namespace missing")
	}
	baseURL := KubeAPIBaseURL()
	url := fmt.Sprintf("%s/api/v1/namespaces/%s", baseURL, namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return labelNamespace(ctx, client, token, namespace)
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("namespace check failed: %s", resp.Status)
	}
	body, err := json.Marshal(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": namespace,
		},
	})
	if err != nil {
		return err
	}
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/namespaces", bytes.NewReader(body))
	if err != nil {
		return err
	}
	createReq.Header.Set("Authorization", "Bearer "+token)
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := client.Do(createReq)
	if err != nil {
		return err
	}
	defer createResp.Body.Close()
	if createResp.StatusCode >= 400 {
		body, _ := io.ReadAll(createResp.Body)
		details := strings.TrimSpace(string(body))
		if details != "" {
			return fmt.Errorf("namespace create failed: %s: %s", createResp.Status, details)
		}
		return fmt.Errorf("namespace create failed: %s", createResp.Status)
	}
	return labelNamespace(ctx, client, token, namespace)
}

func labelNamespace(ctx context.Context, client *http.Client, token, namespace string) error {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"labels": map[string]interface{}{
				"istio-injection": "enabled",
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/api/v1/namespaces/%s", KubeAPIBaseURL(), namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/merge-patch+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("namespace label failed: %s", resp.Status)
	}
	return nil
}
