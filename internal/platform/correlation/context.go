package correlation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
)

type contextKey struct{}

func WithID(ctx context.Context, correlationID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	correlationID = strings.TrimSpace(correlationID)
	if correlationID == "" {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, correlationID)
}

func IDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(contextKey{}).(string)
	return strings.TrimSpace(value)
}

func NewID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "corr-fallback"
	}
	return "corr-" + hex.EncodeToString(raw[:])
}
