package deploy

import (
	"context"
	"net/http"
	"releaseaworker/internal/models"
	"strings"
)

func deleteManagedRepository(ctx context.Context, client *http.Client, cred *models.SCMCredential, svc models.ServiceConfig) error {
	provider := ""
	token := ""
	if cred != nil {
		provider = strings.TrimSpace(cred.Provider)
		token = strings.TrimSpace(cred.Token)
	}

	return DeleteManagedRepository(ctx, client, DeleteInput{
		RepoURL:     svc.RepoURL,
		SourceType:  svc.SourceType,
		Provider:    provider,
		Token:       token,
		RepoManaged: svc.RepoManaged,
	})
}
