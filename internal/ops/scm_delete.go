package ops

import (
	"context"
	"net/http"
	"strings"

	scm "releaseaworker/internal/integrations/scm"
)

func deleteManagedRepository(ctx context.Context, client *http.Client, cred *scmCredential, svc serviceConfig) error {
	provider := ""
	token := ""
	if cred != nil {
		provider = strings.TrimSpace(cred.Provider)
		token = strings.TrimSpace(cred.Token)
	}

	return scm.DeleteManagedRepository(ctx, client, scm.DeleteInput{
		RepoURL:     svc.RepoURL,
		SourceType:  svc.SourceType,
		Provider:    provider,
		Token:       token,
		RepoManaged: svc.RepoManaged,
	})
}
