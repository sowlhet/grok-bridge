package pipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
	"github.com/wlhet/grok-bridge/internal/config"
	dbpkg "github.com/wlhet/grok-bridge/internal/db"
	xai "github.com/wlhet/grok-bridge/internal/executor/xai"
	"github.com/wlhet/grok-bridge/internal/logging"
	"github.com/wlhet/grok-bridge/internal/models"
	"github.com/wlhet/grok-bridge/internal/pipeline"
	"github.com/wlhet/grok-bridge/internal/translate"
)

func openDB(t *testing.T) *account.Store {
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
	return &account.Store{DB: db}
}

func TestHandleSwitchesAccountOn500(t *testing.T) {
	store := openDB(t)
	ctx := context.Background()

	var hits atomic.Int32
	var tokens []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		auth := r.Header.Get("Authorization")
		tokens = append(tokens, auth)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/responses" {
			t.Errorf("path=%q", r.URL.Path)
		}
		if len(body) == 0 {
			t.Error("empty body")
		}
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"boom"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Minimal xAI Responses completed payload.
		_, _ = w.Write([]byte(`{
			"id":"resp_ok",
			"object":"response",
			"model":"grok-4.5",
			"status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}
		}`))
	}))
	defer server.Close()

	// Two active accounts pointing at the fake upstream.
	// Expires far in the future so proactive refresh is not triggered.
	exp := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	a1, err := store.UpsertFromOAuthJSON(ctx, []byte(fmt.Sprintf(
		`{"access_token":"tok-1","refresh_token":"ref-1","email":"a1@x.ai","sub":"s1","expired":%q,"base_url":%q}`,
		exp, server.URL,
	)), true)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := store.UpsertFromOAuthJSON(ctx, []byte(fmt.Sprintf(
		`{"access_token":"tok-2","refresh_token":"ref-2","email":"a2@x.ai","sub":"s2","expired":%q,"base_url":%q}`,
		exp, server.URL,
	)), true)
	if err != nil {
		t.Fatal(err)
	}
	_ = a1
	_ = a2

	logStore := &logging.RequestLogStore{DB: store.DB}
	catalog := models.NewFromConfig(&config.Config{
		Models:  []config.ModelEntry{{ID: "grok-4.5"}},
		Aliases: map[string]string{"gpt-5": "grok-4.5"},
		Proxy:   config.ProxyConfig{UnknownModel: "passthrough"},
	})

	p := &pipeline.Pipeline{
		Accounts:     &account.Picker{Store: store},
		AccountStore: store,
		XAI:          &xai.Client{HTTP: server.Client()},
		OAuth:        nil, // no refresh needed
		Catalog:      catalog,
		Logs:         logStore,
		Retry:        config.RetryConfig{MaxAccountSwitches: 2, MaxTransientRetries: 2},
		LogBodies:    "all",
	}

	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	rr := httptest.NewRecorder()
	in := pipeline.Inbound{
		Protocol:  translate.FormatOpenAIChat,
		Model:     "gpt-5",
		Body:      body,
		Stream:    false,
		APIKey:    &access.KeyRecord{ID: "k1", Label: "test-key"},
		Path:      "/v1/chat/completions",
		ClientIP:  "127.0.0.1",
		UserAgent: "pipeline-test",
	}

	if err := p.Handle(ctx, in, rr); err != nil {
		t.Fatalf("Handle error: %v", err)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits=%d want 2 (first 500, second 200)", hits.Load())
	}
	// Second attempt should use the other token.
	if len(tokens) != 2 {
		t.Fatalf("tokens captured=%v", tokens)
	}
	if tokens[0] == tokens[1] {
		t.Fatalf("expected different account tokens, got %v", tokens)
	}

	var chat map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &chat); err != nil {
		t.Fatalf("response json: %v body=%s", err, rr.Body.String())
	}
	if chat["object"] != "chat.completion" {
		t.Fatalf("object=%v", chat["object"])
	}

	logs, err := logStore.Query(ctx, logging.LogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs len=%d want 1", len(logs))
	}
	lg := logs[0]
	if lg.StatusCode != 200 {
		t.Fatalf("log status=%d", lg.StatusCode)
	}
	if lg.ModelRequested != "gpt-5" || lg.ModelUpstream != "grok-4.5" {
		t.Fatalf("models: req=%q up=%q", lg.ModelRequested, lg.ModelUpstream)
	}
	if lg.APIKeyLabel != "test-key" {
		t.Fatalf("api key label=%q", lg.APIKeyLabel)
	}
	if lg.AccountID == "" {
		t.Fatal("expected account_id in log")
	}
	if lg.RequestBody == "" || lg.ResponseBody == "" {
		t.Fatalf("expected bodies logged (log_bodies=all), req=%q resp=%q", lg.RequestBody, lg.ResponseBody)
	}
	if lg.InputTokens != 3 || lg.OutputTokens != 1 {
		t.Fatalf("tokens in=%d out=%d", lg.InputTokens, lg.OutputTokens)
	}
}

func TestHandleNoAccounts(t *testing.T) {
	store := openDB(t)
	ctx := context.Background()
	logStore := &logging.RequestLogStore{DB: store.DB}

	p := &pipeline.Pipeline{
		Accounts:     &account.Picker{Store: store},
		AccountStore: store,
		XAI:          &xai.Client{HTTP: http.DefaultClient},
		Catalog: models.NewFromConfig(&config.Config{
			Models: []config.ModelEntry{{ID: "grok-4.5"}},
			Proxy:  config.ProxyConfig{UnknownModel: "passthrough"},
		}),
		Logs:      logStore,
		Retry:     config.RetryConfig{MaxAccountSwitches: 2},
		LogBodies: "errors_only",
	}

	rr := httptest.NewRecorder()
	err := p.Handle(ctx, pipeline.Inbound{
		Protocol: translate.FormatOpenAIChat,
		Model:    "grok-4.5",
		Body:     []byte(`{"model":"grok-4.5","messages":[{"role":"user","content":"x"}]}`),
		Path:     "/v1/chat/completions",
	}, rr)
	if err == nil {
		t.Fatal("expected error with no accounts")
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rr.Code)
	}
	logs, qerr := logStore.Query(ctx, logging.LogFilter{})
	if qerr != nil {
		t.Fatal(qerr)
	}
	if len(logs) != 1 || logs[0].StatusCode != 503 {
		t.Fatalf("log=%+v", logs)
	}
	// errors_only should store bodies for 503.
	if logs[0].RequestBody == "" {
		t.Fatal("expected request body logged for error")
	}
}
