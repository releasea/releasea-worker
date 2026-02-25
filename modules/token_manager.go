package ops

import (
	"context"
	"errors"
	"net/http"

	scm "releaseaworker/modules/scm"
)

type tokenManager struct {
	manager *scm.Manager
}

func newTokenManager(token string) *tokenManager {
	return &tokenManager{manager: scm.NewManager(token)}
}

func (tm *tokenManager) get(ctx context.Context, client *http.Client, cfg Config) (string, error) {
	if tm == nil || tm.manager == nil {
		return "", errors.New("token manager not initialized")
	}
	return tm.manager.Get(ctx, client, cfg)
}

func (tm *tokenManager) invalidate() {
	if tm == nil || tm.manager == nil {
		return
	}
	tm.manager.Invalidate()
}

func setAuthHeaders(req *http.Request, token string) {
	scm.SetAuthHeaders(req, token)
}
