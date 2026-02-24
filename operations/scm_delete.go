package operations

import (
	"context"
	"net/http"
	"strings"

	"releaseaworker/integrations"
)

func deleteManagedRepository(ctx context.Context, client *http.Client, cred *scmCredential, svc serviceConfig) error {
	provider := ""
	token := ""
	if cred != nil {
		provider = strings.TrimSpace(cred.Provider)
		token = strings.TrimSpace(cred.Token)
	}

	return integrations.DeleteManagedRepository(ctx, client, integrations.DeleteInput{
		RepoURL:     svc.RepoURL,
		SourceType:  svc.SourceType,
		Provider:    provider,
		Token:       token,
		RepoManaged: svc.RepoManaged,
	})
}
