package scmproviders

import (
	"context"
	"net/http"
	"net/url"
	"releaseaworker/internal/platform/models"
	"strings"
)

type Definition struct {
	ID              string
	TokenUsername   string
	TokenAsPassword bool
}

type Runtime interface {
	ID() string
	InjectCloneCredentials(repoURL string, cred *models.SCMCredential) string
	DeleteManagedRepository(ctx context.Context, client *http.Client, token, repoURL string, repoManaged bool) error
}

var registry = map[string]Definition{
	"github": {
		ID:              "github",
		TokenUsername:   "x-access-token",
		TokenAsPassword: true,
	},
	"gitlab": {
		ID:              "gitlab",
		TokenUsername:   "oauth2",
		TokenAsPassword: true,
	},
	"bitbucket": {
		ID:              "bitbucket",
		TokenUsername:   "x-token-auth",
		TokenAsPassword: true,
	},
}

func Normalize(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "github"
	}
	return provider
}

func Resolve(provider string) (Definition, bool) {
	definition, ok := registry[Normalize(provider)]
	return definition, ok
}

func ResolveRuntime(provider string) (Runtime, bool) {
	definition, ok := Resolve(provider)
	if !ok {
		return nil, false
	}
	return runtime{definition: definition}, true
}

func InjectCloneCredentials(repoURL string, cred *models.SCMCredential) string {
	runtime, ok := ResolveRuntime(credProvider(cred))
	if !ok {
		return repoURL
	}
	return runtime.InjectCloneCredentials(repoURL, cred)
}

type runtime struct {
	definition Definition
}

func (r runtime) ID() string {
	return r.definition.ID
}

func (r runtime) InjectCloneCredentials(repoURL string, cred *models.SCMCredential) string {
	if cred == nil || strings.TrimSpace(cred.Token) == "" || strings.EqualFold(strings.TrimSpace(cred.AuthType), "ssh") {
		return repoURL
	}
	parsed, err := url.Parse(repoURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return repoURL
	}
	if parsed.User != nil && parsed.User.Username() != "" {
		return repoURL
	}
	if r.definition.TokenAsPassword {
		parsed.User = url.UserPassword(r.definition.TokenUsername, cred.Token)
		return parsed.String()
	}
	parsed.User = url.User(cred.Token)
	return parsed.String()
}

func (r runtime) DeleteManagedRepository(ctx context.Context, client *http.Client, token, repoURL string, repoManaged bool) error {
	repo, err := ParseRepoRef(repoURL)
	if err != nil {
		return nil
	}

	managed := repoManaged
	token = strings.TrimSpace(token)
	if r.definition.ID == "github" {
		ok, err := GitHubHasReleaseaMarker(ctx, client, token, repo)
		if err != nil {
			return err
		}
		if ok {
			managed = true
		}
	}
	if !managed {
		return nil
	}
	if token == "" {
		return ErrMissingDeleteToken
	}

	switch r.definition.ID {
	case "github":
		return DeleteGitHubRepo(ctx, client, token, repo)
	case "gitlab":
		return DeleteGitLabRepo(ctx, client, token, repo)
	case "bitbucket":
		return DeleteBitbucketRepo(ctx, client, token, repo)
	default:
		return ErrUnsupportedDeleteProvider(r.definition.ID)
	}
}

func credProvider(cred *models.SCMCredential) string {
	if cred == nil {
		return ""
	}
	return cred.Provider
}
