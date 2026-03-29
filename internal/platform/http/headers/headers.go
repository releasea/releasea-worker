package headers

import (
	"net/http"
	"strings"
)

const (
	HeaderAuthorization = "Authorization"
	HeaderContentType   = "Content-Type"
	HeaderCorrelationID = "X-Correlation-ID"

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

func SetCorrelationID(req *http.Request, correlationID string) {
	if req == nil {
		return
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return
	}
	req.Header.Set(HeaderCorrelationID, correlationID)
}
