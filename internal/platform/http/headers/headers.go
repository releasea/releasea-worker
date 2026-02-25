package headers

import (
	"net/http"
	"strings"
)

const (
	HeaderAuthorization = "Authorization"
	HeaderContentType   = "Content-Type"

	ContentTypeJSON = "application/json"
)

func SetContentTypeJSON(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set(HeaderContentType, ContentTypeJSON)
}

func SetBearerToken(req *http.Request, token string) {
	if req == nil {
		return
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	req.Header.Set(HeaderAuthorization, "Bearer "+token)
}
