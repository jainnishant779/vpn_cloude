package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const (
	contextUserIDKey contextKey = "auth_user_id"
	contextEmailKey  contextKey = "auth_email"
)

// AuthMiddleware verifies bearer tokens and injects identity into context.
func (s *JWTService) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if authHeader == "" {
			writeUnauthorized(w, "missing authorization header")
			return
		}

		const bearerPrefix = "Bearer "
		if !strings.HasPrefix(authHeader, bearerPrefix) {
			writeUnauthorized(w, "invalid authorization header")
			return
		}

		token := strings.TrimSpace(strings.TrimPrefix(authHeader, bearerPrefix))
		claims, err := s.ValidateToken(token)
		if err != nil {
			writeUnauthorized(w, "invalid token")
			return
		}

		ctx := context.WithValue(r.Context(), contextUserIDKey, claims.UserID)
		ctx = context.WithValue(ctx, contextEmailKey, claims.Email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserIDFromContext extracts authenticated user id.
func UserIDFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(contextUserIDKey)
	id, ok := v.(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// EmailFromContext extracts authenticated email when available.
func EmailFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(contextEmailKey)
	email, ok := v.(string)
	if !ok || email == "" {
		return "", false
	}
	return email, true
}

func writeUnauthorized(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"data":    nil,
		"error":   message,
	})
}
