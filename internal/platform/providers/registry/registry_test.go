package registryproviders

import "testing"

func TestResolveLoginHost(t *testing.T) {
	if got := ResolveLoginHost("ghcr", "", "ghcr.io/releasea/worker:latest"); got != "ghcr.io" {
		t.Fatalf("expected ghcr.io, got %q", got)
	}
	if got := ResolveLoginHost("docker", "", "releasea/worker:latest"); got != "docker.io" {
		t.Fatalf("expected docker.io fallback, got %q", got)
	}
	if got := ResolveLoginHost("docker", "https://index.docker.io/v1/", ""); got != "docker.io" {
		t.Fatalf("expected normalized docker.io, got %q", got)
	}
}

func TestResolveRuntime(t *testing.T) {
	runtime := ResolveRuntime("ecr")
	if runtime.ID() != "ecr" {
		t.Fatalf("expected ecr runtime, got %q", runtime.ID())
	}
	if got := runtime.ResolveLoginHost("", ""); got != "docker.io" {
		t.Fatalf("expected docker.io fallback, got %q", got)
	}
}
