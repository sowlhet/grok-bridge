package account_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wlhet/grok-bridge/internal/account"
	dbpkg "github.com/wlhet/grok-bridge/internal/db"
)

func openTestStore(t *testing.T) *account.Store {
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

func TestListGetSetStatusDelete(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	raw := []byte(`{"type":"xai","access_token":"a1","refresh_token":"r1","email":"a@x.ai","sub":"s1"}`)
	a, err := store.UpsertFromOAuthJSON(ctx, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" {
		t.Fatal("expected id")
	}
	if a.Status != "active" {
		t.Fatalf("status=%q want active", a.Status)
	}
	if a.Weight != 1 {
		t.Fatalf("weight=%d want 1", a.Weight)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list=%+v", list)
	}

	got, err := store.Get(ctx, a.ID)
	if err != nil || got == nil || got.AccessToken != "a1" {
		t.Fatalf("get: %+v err=%v", got, err)
	}

	if err := store.SetStatus(ctx, a.ID, "disabled", "manual"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "disabled" || got.ErrorMessage != "manual" {
		t.Fatalf("status=%q err=%q", got.Status, got.ErrorMessage)
	}

	active, err := store.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active, got %d", len(active))
	}

	if err := store.SetStatus(ctx, a.ID, "active", ""); err != nil {
		t.Fatal(err)
	}
	active, err = store.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 {
		t.Fatalf("active len=%d", len(active))
	}

	if err := store.UpdateTokens(ctx, a.ID, "a3", "r3", "id3", "2026-08-01T00:00:00Z", "2026-07-13T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "a3" || got.RefreshToken != "r3" || got.IDToken != "id3" {
		t.Fatalf("tokens not updated: %+v", got)
	}
	if got.ExpiresAt != "2026-08-01T00:00:00Z" || got.LastRefreshAt != "2026-07-13T00:00:00Z" {
		t.Fatalf("expiry fields: expires=%q last=%q", got.ExpiresAt, got.LastRefreshAt)
	}

	if err := store.Delete(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err = store.Get(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestListActiveOnlyActive(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	a1, err := store.UpsertFromOAuthJSON(ctx, []byte(`{"access_token":"a","refresh_token":"r","email":"one@x.ai","sub":"s1"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := store.UpsertFromOAuthJSON(ctx, []byte(`{"access_token":"a","refresh_token":"r","email":"two@x.ai","sub":"s2"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if a2.Status != "disabled" {
		t.Fatalf("enable=false should disable, got %q", a2.Status)
	}

	active, err := store.ListActive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != a1.ID {
		t.Fatalf("active=%+v", active)
	}
}

func TestGetMissing(t *testing.T) {
	store := openTestStore(t)
	got, err := store.Get(context.Background(), "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestDeleteMissing(t *testing.T) {
	store := openTestStore(t)
	err := store.Delete(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSetStatusMissing(t *testing.T) {
	store := openTestStore(t)
	err := store.SetStatus(context.Background(), "missing", "active", "")
	if err == nil {
		t.Fatal("expected error")
	}
}
