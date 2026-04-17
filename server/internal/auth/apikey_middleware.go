package auth

import (
	"context"
	"net/http"
	"strings"
)

const apiKeyHeader = "X-API-Key"

const contextAPIKeyKey contextKey = "auth_api_key"

// APIKeyValidator validates API keys and optionally resolves owner identity.
type APIKeyValidator func(ctx context.Context, apiKey string) (ownerID string, err error)

// APIKeyAuth validates machine-to-machine requests using API keys.
type APIKeyAuth struct {
	validator APIKeyValidator
}

func NewAPIKeyAuth(validator APIKeyValidator) *APIKeyAuth {
	return &APIKeyAuth{validator: validator}
}

// APIKeyMiddleware enforces X-API-Key header and injects identity in context.
func (a *APIKeyAuth) APIKeyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := strings.TrimSpace(r.Header.Get(apiKeyHeader))
		if apiKey == "" {
			writeUnauthorized(w, "missing api key")
			return
		}

		ownerID := ""
		if a != nil && a.validator != nil {
			resolvedOwnerID, err := a.validator(r.Context(), apiKey)
			if err != nil {
				writeUnauthorized(w, "invalid api key")
				return
			}
			ownerID = resolvedOwnerID
		}

		ctx := context.WithValue(r.Context(), contextAPIKeyKey, apiKey)
		if ownerID != "" {
			ctx = context.WithValue(ctx, contextUserIDKey, ownerID)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// APIKeyFromContext extracts the validated API key.
func APIKeyFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(contextAPIKeyKey)
	apiKey, ok := v.(string)
	if !ok || apiKey == "" {
		return "", false
	}
	return apiKey, true
}
