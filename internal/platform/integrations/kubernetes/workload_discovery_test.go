package kubernetes

import "testing"

func TestMapDiscoveredWorkloadExtractsPrimaryContainerSettings(t *testing.T) {
	workload, ok := mapDiscoveredWorkload("Deployment", "releasea-apps-development", map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "payments",
		},
		"spec": map[string]interface{}{
			"replicas": 3,
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "payments",
					},
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":    "payments",
							"image":   "ghcr.io/acme/payments:1.2.3",
							"command": []interface{}{"/bin/payments"},
							"args":    []interface{}{"serve", "--port", "8080"},
							"env": []interface{}{
								map[string]interface{}{"name": "PAYMENTS_MODE", "value": "cluster"},
								map[string]interface{}{"name": "EMPTY_ALLOWED", "value": ""},
								map[string]interface{}{
									"name": "SECRET_ONLY",
									"valueFrom": map[string]interface{}{
										"secretKeyRef": map[string]interface{}{"name": "payments-secret", "key": "token"},
									},
								},
							},
							"ports": []interface{}{
								map[string]interface{}{"containerPort": 8080},
							},
							"resources": map[string]interface{}{
								"requests": map[string]interface{}{
									"cpu":    "500m",
									"memory": "512Mi",
								},
							},
							"readinessProbe": map[string]interface{}{
								"httpGet": map[string]interface{}{"path": "/ready", "port": 8080},
							},
							"livenessProbe": map[string]interface{}{
								"tcpSocket": map[string]interface{}{"port": 8080},
							},
						},
						map[string]interface{}{
							"name":  "metrics-sidecar",
							"image": "ghcr.io/acme/metrics:0.4.0",
							"ports": []interface{}{
								map[string]interface{}{"containerPort": 9090},
							},
						},
					},
				},
			},
		},
	})
	if !ok {
		t.Fatalf("expected workload to be discovered")
	}

	if workload.PrimaryImage != "ghcr.io/acme/payments:1.2.3" {
		t.Fatalf("primary image = %q, want %q", workload.PrimaryImage, "ghcr.io/acme/payments:1.2.3")
	}
	if len(workload.Containers) != 2 {
		t.Fatalf("container count = %d, want %d", len(workload.Containers), 2)
	}
	if !workload.Containers[0].Imported || workload.Containers[1].Imported {
		t.Fatalf("container import flags = %+v, want first imported only", workload.Containers)
	}
	if workload.Port != 8080 {
		t.Fatalf("port = %d, want %d", workload.Port, 8080)
	}
	if workload.HealthCheckPath != "/ready" {
		t.Fatalf("health check path = %q, want %q", workload.HealthCheckPath, "/ready")
	}
	if len(workload.Probes) != 2 {
		t.Fatalf("probe count = %d, want %d", len(workload.Probes), 2)
	}
	if workload.Probes[0].Type != "readiness" || workload.Probes[0].Handler != "httpGet" {
		t.Fatalf("first probe = %+v, want readiness httpGet", workload.Probes[0])
	}
	if workload.Probes[1].Type != "liveness" || workload.Probes[1].Handler != "tcpSocket" {
		t.Fatalf("second probe = %+v, want liveness tcpSocket", workload.Probes[1])
	}
	if workload.Replicas != 3 {
		t.Fatalf("replicas = %d, want %d", workload.Replicas, 3)
	}
	if len(workload.EnvironmentVariables) != 3 {
		t.Fatalf("env var count = %d, want %d", len(workload.EnvironmentVariables), 3)
	}
	if workload.EnvironmentVariables[0].Key != "PAYMENTS_MODE" || workload.EnvironmentVariables[0].Value != "cluster" || !workload.EnvironmentVariables[0].Importable {
		t.Fatalf("first env var = %+v, want PAYMENTS_MODE=cluster", workload.EnvironmentVariables[0])
	}
	if workload.EnvironmentVariables[1].Key != "EMPTY_ALLOWED" {
		t.Fatalf("second env var key = %q, want %q", workload.EnvironmentVariables[1].Key, "EMPTY_ALLOWED")
	}
	if workload.EnvironmentVariables[2].Key != "SECRET_ONLY" {
		t.Fatalf("third env var key = %q, want %q", workload.EnvironmentVariables[2].Key, "SECRET_ONLY")
	}
	if workload.EnvironmentVariables[2].SourceType != "secretKeyRef" || workload.EnvironmentVariables[2].Importable {
		t.Fatalf("third env var = %+v, want non-importable secretKeyRef", workload.EnvironmentVariables[2])
	}
	if workload.EnvironmentVariables[2].Reference != "secret:payments-secret#token" {
		t.Fatalf("third env ref = %q, want %q", workload.EnvironmentVariables[2].Reference, "secret:payments-secret#token")
	}
	if len(workload.Command) != 1 || workload.Command[0] != "/bin/payments" {
		t.Fatalf("command = %#v, want []string{\"/bin/payments\"}", workload.Command)
	}
	if len(workload.Args) != 3 || workload.Args[0] != "serve" {
		t.Fatalf("args = %#v, want command args", workload.Args)
	}
	if workload.CPUMilli != 500 {
		t.Fatalf("cpu milli = %d, want %d", workload.CPUMilli, 500)
	}
	if workload.MemoryMi != 512 {
		t.Fatalf("memory Mi = %d, want %d", workload.MemoryMi, 512)
	}
}

func TestDiscoveryHintsMatchServicesAndIngresses(t *testing.T) {
	podLabels := extractWorkloadPodLabels("Deployment", map[string]interface{}{
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"labels": map[string]interface{}{
						"app": "payments",
					},
				},
			},
		},
	})

	serviceHints := matchDiscoveredServices([]map[string]interface{}{
		{
			"metadata": map[string]interface{}{"name": "payments"},
			"spec": map[string]interface{}{
				"type":      "ClusterIP",
				"clusterIP": "10.0.0.10",
				"selector":  map[string]interface{}{"app": "payments"},
				"ports": []interface{}{
					map[string]interface{}{"port": 80},
					map[string]interface{}{"port": 8080},
				},
			},
		},
	}, podLabels)

	if len(serviceHints) != 1 {
		t.Fatalf("service hint count = %d, want %d", len(serviceHints), 1)
	}
	if serviceHints[0].Name != "payments" {
		t.Fatalf("service hint name = %q, want %q", serviceHints[0].Name, "payments")
	}
	if len(serviceHints[0].Ports) != 2 || serviceHints[0].Ports[1] != 8080 {
		t.Fatalf("service hint ports = %#v, want [80 8080]", serviceHints[0].Ports)
	}

	ingressHints := matchDiscoveredIngresses([]map[string]interface{}{
		{
			"metadata": map[string]interface{}{"name": "payments"},
			"spec": map[string]interface{}{
				"tls": []interface{}{map[string]interface{}{"hosts": []interface{}{"payments.example.com"}}},
				"rules": []interface{}{
					map[string]interface{}{
						"host": "payments.example.com",
						"http": map[string]interface{}{
							"paths": []interface{}{
								map[string]interface{}{
									"path": "/",
									"backend": map[string]interface{}{
										"service": map[string]interface{}{"name": "payments"},
									},
								},
							},
						},
					},
				},
			},
		},
	}, serviceHints)

	if len(ingressHints) != 1 {
		t.Fatalf("ingress hint count = %d, want %d", len(ingressHints), 1)
	}
	if ingressHints[0].Name != "payments" {
		t.Fatalf("ingress hint name = %q, want %q", ingressHints[0].Name, "payments")
	}
	if len(ingressHints[0].Hosts) != 1 || ingressHints[0].Hosts[0] != "payments.example.com" {
		t.Fatalf("ingress hosts = %#v, want []string{\"payments.example.com\"}", ingressHints[0].Hosts)
	}
	if !ingressHints[0].TLS {
		t.Fatalf("ingress TLS = false, want true")
	}
}

func TestParseResourceQuantities(t *testing.T) {
	if got := parseCPUMilli("1"); got != 1000 {
		t.Fatalf("parseCPUMilli(1) = %d, want %d", got, 1000)
	}
	if got := parseCPUMilli("250m"); got != 250 {
		t.Fatalf("parseCPUMilli(250m) = %d, want %d", got, 250)
	}
	if got := parseMemoryMi("1Gi"); got != 1024 {
		t.Fatalf("parseMemoryMi(1Gi) = %d, want %d", got, 1024)
	}
	if got := parseMemoryMi("512Mi"); got != 512 {
		t.Fatalf("parseMemoryMi(512Mi) = %d, want %d", got, 512)
	}
}
