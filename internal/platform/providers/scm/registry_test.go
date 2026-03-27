package scmproviders

import (
	"releaseaworker/internal/platform/models"
	"testing"
)

func TestInjectCloneCredentials(t *testing.T) {
	githubURL := InjectCloneCredentials("https://github.com/releasea/worker.git", &models.SCMCredential{Provider: "github", Token: "abc"})
	if githubURL != "https://x-access-token:abc@github.com/releasea/worker.git" {
		t.Fatalf("unexpected github auth url %q", githubURL)
	}

	sshURL := InjectCloneCredentials("https://github.com/releasea/worker.git", &models.SCMCredential{Provider: "github", AuthType: "ssh", Token: "abc"})
	if sshURL != "https://github.com/releasea/worker.git" {
		t.Fatalf("ssh auth should not inject token, got %q", sshURL)
	}
}

func TestResolveRuntime(t *testing.T) {
	runtime, ok := ResolveRuntime("gitlab")
	if !ok {
		t.Fatalf("expected gitlab runtime")
	}
	if runtime.ID() != "gitlab" {
		t.Fatalf("expected gitlab runtime id, got %q", runtime.ID())
	}
}
