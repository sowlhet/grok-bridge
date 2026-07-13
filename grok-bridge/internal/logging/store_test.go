package logging_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	dbpkg "github.com/wlhet/grok-bridge/internal/db"
	"github.com/wlhet/grok-bridge/internal/logging"
)

func openTestStore(t *testing.T) *logging.RequestLogStore {
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
	return &logging.RequestLogStore{DB: db}
}

func TestInsertAndQuery(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	rec := logging.LogRecord{
		RequestID:      "req-1",
		APIKeyID:       "key-1",
		APIKeyLabel:    "dev",
		AccountID:      "acc-1",
		AccountLabel:   "a@x.ai",
		Protocol:       "openai_chat",
		ModelRequested: "gpt-5",
		ModelUpstream:  "grok-4.5",
		Stream:         false,
		StatusCode:     200,
		LatencyMs:      42,
		InputTokens:    10,
		OutputTokens:   20,
		ClientIP:       "127.0.0.1",
		UserAgent:      "test",
		Path:           "/v1/chat/completions",
		RequestBody:    `{"model":"gpt-5"}`,
		ResponseBody:   `{"id":"r1"}`,
	}
	if err := store.Insert(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// Second row for filter isolation.
	if err := store.Insert(ctx, logging.LogRecord{
		RequestID:      "req-2",
		AccountID:      "acc-2",
		AccountLabel:   "b@x.ai",
		Protocol:       "claude",
		ModelRequested: "claude-x",
		ModelUpstream:  "grok-4.3",
		StatusCode:     500,
		ErrorMessage:   "upstream boom",
		Stream:         true,
	}); err != nil {
		t.Fatal(err)
	}

	all, err := store.Query(ctx, logging.LogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all len=%d want 2", len(all))
	}
	// Newest first; second insert wins when created_at equal within second — either order ok by count.
	byID := map[string]logging.LogRecord{}
	for _, r := range all {
		byID[r.RequestID] = r
		if r.ID == "" {
			t.Fatal("expected auto id")
		}
		if r.CreatedAt == "" {
			t.Fatal("expected auto created_at")
		}
	}
	got := byID["req-1"]
	if got.APIKeyLabel != "dev" || got.ModelUpstream != "grok-4.5" || got.StatusCode != 200 {
		t.Fatalf("req-1 mismatch: %+v", got)
	}
	if got.RequestBody != `{"model":"gpt-5"}` || got.ResponseBody != `{"id":"r1"}` {
		t.Fatalf("bodies: req=%q resp=%q", got.RequestBody, got.ResponseBody)
	}
	if got.LatencyMs != 42 || got.InputTokens != 10 || got.OutputTokens != 20 {
		t.Fatalf("metrics: %+v", got)
	}
	if got.Stream {
		t.Fatal("req-1 stream should be false")
	}

	filtered, err := store.Query(ctx, logging.LogFilter{AccountID: "acc-2", StatusCode: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].RequestID != "req-2" {
		t.Fatalf("filtered=%+v", filtered)
	}
	if !filtered[0].Stream {
		t.Fatal("req-2 stream should be true")
	}

	streamTrue := true
	streamed, err := store.Query(ctx, logging.LogFilter{Stream: &streamTrue})
	if err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 1 || streamed[0].RequestID != "req-2" {
		t.Fatalf("stream filter=%+v", streamed)
	}

	byModel, err := store.Query(ctx, logging.LogFilter{Model: "grok-4.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byModel) != 1 || byModel[0].RequestID != "req-1" {
		t.Fatalf("model filter=%+v", byModel)
	}
}

func TestDashboard(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Seed an active account for ActiveAccounts count.
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := store.DB.ExecContext(ctx, `
INSERT INTO accounts (
  id, label, email, subject, access_token, refresh_token, id_token,
  token_type, expires_at, last_refresh_at, base_url, token_endpoint,
  status, error_message, weight, created_at, updated_at
) VALUES ('a1', 'alice', 'a@x.ai', 's1', 't', 'r', '',
  'Bearer', '', '', '', '', 'active', '', 1, ?, ?)
`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB.ExecContext(ctx, `
INSERT INTO accounts (
  id, label, email, subject, access_token, refresh_token, id_token,
  token_type, expires_at, last_refresh_at, base_url, token_endpoint,
  status, error_message, weight, created_at, updated_at
) VALUES ('a2', 'bob', 'b@x.ai', 's2', 't', 'r', '',
  'Bearer', '', '', '', '', 'disabled', '', 1, ?, ?)
`, now, now)
	if err != nil {
		t.Fatal(err)
	}

	// Today success + error; one older error outside 7d should not count in 7d.
	today := time.Now().UTC().Format(time.RFC3339)
	old := time.Now().UTC().AddDate(0, 0, -10).Format(time.RFC3339)

	for _, rec := range []logging.LogRecord{
		{RequestID: "t1", CreatedAt: today, AccountLabel: "alice", ModelUpstream: "grok-4.5", StatusCode: 200},
		{RequestID: "t2", CreatedAt: today, AccountLabel: "alice", ModelUpstream: "grok-4.5", StatusCode: 500},
		{RequestID: "t3", CreatedAt: today, AccountLabel: "bob", ModelUpstream: "grok-4.3", StatusCode: 200},
		{RequestID: "old", CreatedAt: old, AccountLabel: "alice", ModelUpstream: "grok-4.5", StatusCode: 500},
	} {
		if err := store.Insert(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	st, err := store.Dashboard(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.TodayCount != 3 {
		t.Fatalf("TodayCount=%d want 3", st.TodayCount)
	}
	if st.TodayErrors != 1 {
		t.Fatalf("TodayErrors=%d want 1", st.TodayErrors)
	}
	if st.Last7dCount != 3 {
		t.Fatalf("Last7dCount=%d want 3", st.Last7dCount)
	}
	if st.Last7dErrors != 1 {
		t.Fatalf("Last7dErrors=%d want 1", st.Last7dErrors)
	}
	if st.ActiveAccounts != 1 {
		t.Fatalf("ActiveAccounts=%d want 1", st.ActiveAccounts)
	}
	if len(st.TopModels) == 0 || st.TopModels[0].Name != "grok-4.5" || st.TopModels[0].Count != 2 {
		t.Fatalf("TopModels=%+v", st.TopModels)
	}
	if len(st.TopAccounts) == 0 || st.TopAccounts[0].Name != "alice" || st.TopAccounts[0].Count != 2 {
		t.Fatalf("TopAccounts=%+v", st.TopAccounts)
	}
}

func TestDeleteOlderThan(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	old := time.Now().UTC().AddDate(0, 0, -40).Format(time.RFC3339)
	recent := time.Now().UTC().Format(time.RFC3339)
	if err := store.Insert(ctx, logging.LogRecord{RequestID: "old", CreatedAt: old, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}
	if err := store.Insert(ctx, logging.LogRecord{RequestID: "new", CreatedAt: recent, StatusCode: 200}); err != nil {
		t.Fatal(err)
	}

	n, err := store.DeleteOlderThanDays(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("deleted=%d want 1", n)
	}
	all, err := store.Query(ctx, logging.LogFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].RequestID != "new" {
		t.Fatalf("remaining=%+v", all)
	}
	// retention <= 0 is a no-op
	n, err = store.DeleteOlderThanDays(ctx, 0)
	if err != nil || n != 0 {
		t.Fatalf("noop delete: n=%d err=%v", n, err)
	}
}
