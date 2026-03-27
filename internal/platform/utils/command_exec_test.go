package utils

import (
	"reflect"
	"releaseaworker/internal/platform/models"
	"testing"
)

func TestSplitOutputLines(t *testing.T) {
	output := []byte("line1\r\n\r\nline2\n line3 \r")
	got := splitOutputLines(output)
	want := []string{"line1", "line2", " line3 "}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestInjectToken(t *testing.T) {
	url := "https://github.com/releasea/worker.git"
	cred := &models.SCMCredential{Provider: "github", Token: "abc123"}
	withToken := InjectToken(url, cred)
	if withToken == url {
		t.Fatalf("expected url with injected token")
	}
	if want := "https://x-access-token:abc123@github.com/releasea/worker.git"; withToken != want {
		t.Fatalf("expected github auth url %q, got %q", want, withToken)
	}

	gitlab := InjectToken("https://gitlab.com/releasea/worker.git", &models.SCMCredential{Provider: "gitlab", Token: "xyz"})
	if want := "https://oauth2:xyz@gitlab.com/releasea/worker.git"; gitlab != want {
		t.Fatalf("expected gitlab auth url %q, got %q", want, gitlab)
	}

	bitbucket := InjectToken("https://bitbucket.org/releasea/worker.git", &models.SCMCredential{Provider: "bitbucket", Token: "pqr"})
	if want := "https://x-token-auth:pqr@bitbucket.org/releasea/worker.git"; bitbucket != want {
		t.Fatalf("expected bitbucket auth url %q, got %q", want, bitbucket)
	}

	if got := InjectToken(url, nil); got != url {
		t.Fatalf("expected original url without credentials")
	}
}

func TestRegistryHelpers(t *testing.T) {
	if got := RegistryFromImage("docker.io/releasea/worker:latest"); got != "docker.io" {
		t.Fatalf("expected docker.io, got %q", got)
	}
	if got := RegistryFromImage("releasea/worker:latest"); got != "" {
		t.Fatalf("expected empty registry for short image, got %q", got)
	}

	if got := NormalizeRegistryHost("https://index.docker.io/v1/"); got != "docker.io" {
		t.Fatalf("expected normalized docker.io host, got %q", got)
	}
	if got := NormalizeRegistryHost(" registry.example.com/v1/ "); got != "registry.example.com" {
		t.Fatalf("expected normalized host, got %q", got)
	}
}
