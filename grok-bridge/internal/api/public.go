package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/wlhet/grok-bridge/internal/pipeline"
	"github.com/wlhet/grok-bridge/internal/translate"
)

// registerPublicRoutesOn mounts authenticated proxy endpoints on mux.
func (s *Server) registerPublicRoutesOn(mux *http.ServeMux) {
	// Claude Messages
	mux.HandleFunc("POST /v1/messages", s.requireAPIKey(s.handleMessages))
	mux.HandleFunc("POST /openai/v1/messages", s.requireAPIKey(s.handleMessages))

	// Token count (best-effort)
	mux.HandleFunc("POST /v1/messages/count_tokens", s.requireAPIKey(s.handleCountTokens))
	mux.HandleFunc("POST /openai/v1/messages/count_tokens", s.requireAPIKey(s.handleCountTokens))

	// OpenAI Chat Completions
	mux.HandleFunc("POST /v1/chat/completions", s.requireAPIKey(s.handleChatCompletions))
	mux.HandleFunc("POST /openai/v1/chat/completions", s.requireAPIKey(s.handleChatCompletions))

	// OpenAI / xAI Responses
	mux.HandleFunc("POST /v1/responses", s.requireAPIKey(s.handleResponses))
	mux.HandleFunc("POST /openai/v1/responses", s.requireAPIKey(s.handleResponses))

	// Compact — not implemented in v1
	mux.HandleFunc("POST /v1/responses/compact", s.requireAPIKey(s.handleResponsesCompact))
	mux.HandleFunc("POST /openai/v1/responses/compact", s.requireAPIKey(s.handleResponsesCompact))

	// Models list
	mux.HandleFunc("GET /v1/models", s.requireAPIKey(s.handleModels))
	mux.HandleFunc("GET /openai/v1/models", s.requireAPIKey(s.handleModels))
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	s.handleProxy(w, r, translate.FormatClaude)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	s.handleProxy(w, r, translate.FormatOpenAIChat)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.handleProxy(w, r, translate.FormatOpenAIResponses)
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request, protocol translate.Format) {
	if s.pipeline == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"code":    "not_configured",
				"message": "pipeline not configured",
				"type":    "server_error",
			},
		})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20)) // 32 MiB
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":    "invalid_request",
				"message": "failed to read body",
				"type":    "invalid_request_error",
			},
		})
		return
	}
	_ = r.Body.Close()

	model, stream := parseModelAndStream(body)
	in := pipeline.Inbound{
		Protocol:  protocol,
		Model:     model,
		Body:      body,
		Stream:    stream,
		APIKey:    APIKeyFromContext(r.Context()),
		Path:      r.URL.Path,
		ClientIP:  clientIP(r),
		UserAgent: r.UserAgent(),
	}
	// Pipeline writes the response (including errors).
	_ = s.pipeline.Handle(r.Context(), in, w)
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":    "invalid_request",
				"message": "failed to read body",
				"type":    "invalid_request_error",
			},
		})
		return
	}
	_ = r.Body.Close()

	// Rough char/4 estimate over the raw JSON body (best-effort v1).
	n := (len(body) + 3) / 4
	if n < 1 {
		n = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"input_tokens": n,
	})
}

func (s *Server) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": "compact not implemented",
	})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if s.catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]any{
				"code":    "not_configured",
				"message": "model catalog not configured",
				"type":    "server_error",
			},
		})
		return
	}
	// Claude clients send anthropic-version; return Anthropic-ish list shape.
	if r.Header.Get("anthropic-version") != "" {
		writeJSON(w, http.StatusOK, s.catalog.ListClaude())
		return
	}
	writeJSON(w, http.StatusOK, s.catalog.ListOpenAI())
}

func parseModelAndStream(body []byte) (model string, stream bool) {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil || m == nil {
		return "", false
	}
	if v, ok := m["model"].(string); ok {
		model = v
	}
	switch v := m["stream"].(type) {
	case bool:
		stream = v
	case string:
		stream = strings.EqualFold(v, "true") || v == "1"
	}
	return model, stream
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First hop is the original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
