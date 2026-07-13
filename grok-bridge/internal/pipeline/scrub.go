package pipeline

import (
	"encoding/json"
	"regexp"
	"strings"
)

var (
	// Authorization: Bearer <token>
	reBearer = regexp.MustCompile(`(?i)(Authorization\s*:\s*Bearer\s+)(\S+)`)
	// x-api-key: <key> (header-ish or free text)
	reXAPIKeyHeader = regexp.MustCompile(`(?i)(x-api-key\s*[:=]\s*)([^\s"',}]+)`)
	// JSON string fields that commonly hold secrets.
	reJSONSecretField = regexp.MustCompile(
		`(?i)("(?:access_token|refresh_token|id_token|api_key|authorization|x-api-key|password|client_secret)"\s*:\s*")([^"]*)(")`,
	)
	// Bearer tokens embedded in JSON strings or free text.
	reBearerInline = regexp.MustCompile(`(?i)(Bearer\s+)([A-Za-z0-9\-._~+/]+=*)`)
)

const redacted = "[REDACTED]"

// ScrubSecrets redacts common credential patterns from logged request/response bodies.
// Safe for non-JSON free text and SSE fragments; best-effort, not a full parser.
func ScrubSecrets(s string) string {
	if s == "" {
		return s
	}
	out := s
	out = reBearer.ReplaceAllString(out, `${1}`+redacted)
	out = reBearerInline.ReplaceAllString(out, `${1}`+redacted)
	out = reXAPIKeyHeader.ReplaceAllString(out, `${1}`+redacted)
	out = reJSONSecretField.ReplaceAllString(out, `${1}`+redacted+`${3}`)

	// If the whole body is JSON, also walk maps for nested secret keys.
	if strings.HasPrefix(strings.TrimSpace(out), "{") || strings.HasPrefix(strings.TrimSpace(out), "[") {
		var v any
		if err := json.Unmarshal([]byte(out), &v); err == nil {
			scrubJSONValue(v)
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
	}
	return out
}

func scrubJSONValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			lk := strings.ToLower(k)
			switch lk {
			case "access_token", "refresh_token", "id_token", "api_key", "authorization",
				"x-api-key", "password", "client_secret", "token":
				t[k] = redacted
			default:
				scrubJSONValue(val)
			}
		}
	case []any:
		for _, item := range t {
			scrubJSONValue(item)
		}
	}
}
