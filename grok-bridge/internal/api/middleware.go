package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wlhet/grok-bridge/internal/access"
)

type ctxKey int

const apiKeyCtxKey ctxKey = 1

// APIKeyFromContext returns the verified KeyRecord attached by requireAPIKey.
func APIKeyFromContext(ctx context.Context) *access.KeyRecord {
	v, _ := ctx.Value(apiKeyCtxKey).(*access.KeyRecord)
	return v
}

// extractAPIKey reads Authorization: Bearer <key> or x-api-key.
// Bearer takes precedence when both are present.
func extractAPIKey(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			return strings.TrimSpace(auth[len(prefix):])
		}
		// Some clients send the raw key as Authorization without Bearer.
		if !strings.Contains(auth, " ") {
			return strings.TrimSpace(auth)
		}
	}
	if k := r.Header.Get("x-api-key"); k != "" {
		return strings.TrimSpace(k)
	}
	return ""
}

// requireAPIKey verifies the client key and injects *access.KeyRecord into context.
func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.keys == nil {
			writeAuthError(w, "api key store not configured")
			return
		}
		plain := extractAPIKey(r)
		if plain == "" {
			writeAuthError(w, "missing api key")
			return
		}
		rec, err := s.keys.Verify(r.Context(), plain)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": map[string]any{
					"code":    "internal_error",
					"message": "failed to verify api key",
					"type":    "internal_error",
				},
			})
			return
		}
		if rec == nil {
			writeAuthError(w, "invalid api key")
			return
		}
		ctx := context.WithValue(r.Context(), apiKeyCtxKey, rec)
		next(w, r.WithContext(ctx))
	}
}

func writeAuthError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusUnauthorized, map[string]any{
		"error": map[string]any{
			"code":    "unauthorized",
			"message": msg,
			"type":    "authentication_error",
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
