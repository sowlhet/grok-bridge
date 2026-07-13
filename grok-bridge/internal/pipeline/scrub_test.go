package pipeline_test

import (
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/pipeline"
)

func TestScrubSecretsJSONTokens(t *testing.T) {
	in := `{"access_token":"secret-at","refresh_token":"secret-rt","id_token":"secret-id","model":"grok-4.5"}`
	out := pipeline.ScrubSecrets(in)
	if strings.Contains(out, "secret-at") || strings.Contains(out, "secret-rt") || strings.Contains(out, "secret-id") {
		t.Fatalf("tokens not redacted: %s", out)
	}
	if !strings.Contains(out, "grok-4.5") {
		t.Fatalf("non-secret field lost: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redacted marker: %s", out)
	}
}

func TestScrubSecretsBearerAndAPIKey(t *testing.T) {
	in := "Authorization: Bearer sk-live-abc123\nx-api-key: gb_supersecret\nBearer another-token-xyz"
	out := pipeline.ScrubSecrets(in)
	if strings.Contains(out, "sk-live-abc123") || strings.Contains(out, "gb_supersecret") || strings.Contains(out, "another-token-xyz") {
		t.Fatalf("credentials remain: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected redacted marker: %s", out)
	}
}

func TestScrubSecretsNestedJSON(t *testing.T) {
	in := `{"auth":{"access_token":"nested-secret"},"ok":true}`
	out := pipeline.ScrubSecrets(in)
	if strings.Contains(out, "nested-secret") {
		t.Fatalf("nested token not redacted: %s", out)
	}
}
