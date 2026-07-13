package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
	"github.com/wlhet/grok-bridge/internal/api"
	"github.com/wlhet/grok-bridge/internal/config"
	dbpkg "github.com/wlhet/grok-bridge/internal/db"
	xai "github.com/wlhet/grok-bridge/internal/executor/xai"
	"github.com/wlhet/grok-bridge/internal/logging"
	"github.com/wlhet/grok-bridge/internal/models"
	"github.com/wlhet/grok-bridge/internal/pipeline"
)

func TestHealthz(t *testing.T) {
	s := api.NewServer(api.ServerDeps{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

func openTestServer(t *testing.T) (handler http.Handler, plaintextKey string, upstream *httptest.Server) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := dbpkg.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := dbpkg.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	keys := &access.KeyStore{DB: db}
	plain, _, err := keys.Create(ctx, "test-key")
	if err != nil {
		t.Fatal(err)
	}

	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			http.Error(w, "empty", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"resp_ok",
			"object":"response",
			"model":"grok-4.5",
			"status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}
		}`))
	}))
	t.Cleanup(upstream.Close)

	accStore := &account.Store{DB: db}
	exp := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	if _, err := accStore.UpsertFromOAuthJSON(ctx, []byte(fmt.Sprintf(
		`{"access_token":"tok-1","refresh_token":"ref-1","email":"a1@x.ai","sub":"s1","expired":%q,"base_url":%q}`,
		exp, upstream.URL,
	)), true); err != nil {
		t.Fatal(err)
	}

	catalog := models.NewFromConfig(&config.Config{
		Models:  []config.ModelEntry{{ID: "grok-4.5"}, {ID: "grok-3-mini"}},
		Aliases: map[string]string{"gpt-5": "grok-4.5"},
		Proxy:   config.ProxyConfig{UnknownModel: "passthrough"},
	})

	p := &pipeline.Pipeline{
		Accounts:     &account.Picker{Store: accStore},
		AccountStore: accStore,
		XAI:          &xai.Client{HTTP: upstream.Client()},
		Catalog:      catalog,
		Logs:         &logging.RequestLogStore{DB: db},
		Retry:        config.RetryConfig{MaxAccountSwitches: 1, MaxTransientRetries: 1},
		LogBodies:    "errors_only",
	}

	s := api.NewServer(api.ServerDeps{
		Pipeline: p,
		Keys:     keys,
		Catalog:  catalog,
	})
	return s.Handler(), plain, upstream
}

func TestChatCompletionsRequiresAPIKey(t *testing.T) {
	h, _, _ := openTestServer(t)
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatCompletionsOK(t *testing.T) {
	h, key, _ := openTestServer(t)
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var chat map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &chat); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	if chat["object"] != "chat.completion" {
		t.Fatalf("object=%v", chat["object"])
	}
}

func TestChatCompletionsOpenAIPrefix(t *testing.T) {
	h, key, _ := openTestServer(t)
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestChatCompletionsXAPIKey(t *testing.T) {
	h, key, _ := openTestServer(t)
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestInvalidAPIKey(t *testing.T) {
	h, _, _ := openTestServer(t)
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer gb_invalid")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMessagesOK(t *testing.T) {
	h, key, _ := openTestServer(t)
	body := []byte(`{"model":"grok-4.5","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var msg map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &msg); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	if msg["type"] != "message" {
		t.Fatalf("type=%v body=%s", msg["type"], rr.Body.String())
	}
}

func TestResponsesOK(t *testing.T) {
	h, key, _ := openTestServer(t)
	body := []byte(`{"model":"grok-4.5","input":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestResponsesCompact501(t *testing.T) {
	h, key, _ := openTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if body["error"] != "compact not implemented" {
		t.Fatalf("error=%v", body["error"])
	}
}

func TestCountTokensEstimate(t *testing.T) {
	h, key, _ := openTestServer(t)
	// ~40 chars → ~10 tokens at char/4.
	body := []byte(`{"model":"grok-4.5","messages":[{"role":"user","content":"hello world token estimate"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	n, ok := out["input_tokens"].(float64)
	if !ok || n <= 0 {
		t.Fatalf("input_tokens=%v", out["input_tokens"])
	}
}

func TestModelsOpenAIShape(t *testing.T) {
	h, key, _ := openTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if out["object"] != "list" {
		t.Fatalf("object=%v", out["object"])
	}
}

func TestModelsClaudeShape(t *testing.T) {
	h, key, _ := openTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/openai/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("anthropic-version", "2023-06-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v", err)
	}
	if _, ok := out["object"]; ok {
		t.Fatalf("unexpected openai object field in claude shape: %v", out)
	}
	data, ok := out["data"].([]any)
	if !ok || len(data) == 0 {
		t.Fatalf("data=%v", out["data"])
	}
}

func TestHealthzNoAuth(t *testing.T) {
	h, _, _ := openTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
}
