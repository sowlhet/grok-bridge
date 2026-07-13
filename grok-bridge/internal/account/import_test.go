package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestImportUpsertByEmail(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	raw := []byte(`{"type":"xai","access_token":"a1","refresh_token":"r1","email":"a@x.ai","sub":"s1"}`)
	a1, err := store.UpsertFromOAuthJSON(ctx, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	raw2 := []byte(`{"type":"xai","access_token":"a2","refresh_token":"r2","email":"a@x.ai","sub":"s1"}`)
	a2, err := store.UpsertFromOAuthJSON(ctx, raw2, true)
	if err != nil {
		t.Fatal(err)
	}
	if a1.ID != a2.ID {
		t.Fatal("expected same id")
	}
	if a2.AccessToken != "a2" {
		t.Fatal("token not updated")
	}
	out, err := store.ExportJSON(ctx, a2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"type": "xai"`)) && !bytes.Contains(out, []byte(`"type":"xai"`)) {
		t.Fatalf("export missing type: %s", out)
	}
	if !bytes.Contains(out, []byte(`"auth_kind": "oauth"`)) && !bytes.Contains(out, []byte(`"auth_kind":"oauth"`)) {
		t.Fatalf("export missing auth_kind: %s", out)
	}
	if !bytes.Contains(out, []byte(`"access_token"`)) {
		t.Fatalf("export missing access_token: %s", out)
	}
}

func TestUpsertBySubjectWhenEmailEmpty(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	raw := []byte(`{"type":"xai","access_token":"a1","refresh_token":"r1","sub":"only-sub"}`)
	a1, err := store.UpsertFromOAuthJSON(ctx, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	raw2 := []byte(`{"type":"xai","access_token":"a2","refresh_token":"r2","sub":"only-sub"}`)
	a2, err := store.UpsertFromOAuthJSON(ctx, raw2, true)
	if err != nil {
		t.Fatal(err)
	}
	if a1.ID != a2.ID {
		t.Fatal("expected same id for same sub")
	}
	if a2.AccessToken != "a2" {
		t.Fatal("token not updated")
	}
}

func TestImportManyArrayAndSingle(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	// Array: two new accounts
	payload := []byte(`[
		{"type":"xai","access_token":"a1","refresh_token":"r1","email":"one@x.ai","sub":"s1"},
		{"type":"xai","access_token":"a2","refresh_token":"r2","email":"two@x.ai","sub":"s2"}
	]`)
	ins, upd, err := store.ImportMany(ctx, payload, true)
	if err != nil {
		t.Fatal(err)
	}
	if ins != 2 || upd != 0 {
		t.Fatalf("inserted=%d updated=%d want 2,0", ins, upd)
	}

	// Re-import first as single object → update
	single := []byte(`{"type":"xai","access_token":"a1b","refresh_token":"r1b","email":"one@x.ai","sub":"s1"}`)
	ins, upd, err = store.ImportMany(ctx, single, true)
	if err != nil {
		t.Fatal(err)
	}
	if ins != 0 || upd != 1 {
		t.Fatalf("inserted=%d updated=%d want 0,1", ins, upd)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d", len(list))
	}
	var found bool
	for _, a := range list {
		if a.Email == "one@x.ai" {
			found = true
			if a.AccessToken != "a1b" {
				t.Fatalf("token=%q", a.AccessToken)
			}
		}
	}
	if !found {
		t.Fatal("missing one@x.ai")
	}
}

func TestExportRoundTripFields(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	raw := []byte(`{
		"type":"xai",
		"auth_kind":"oauth",
		"access_token":"atok",
		"refresh_token":"rtok",
		"id_token":"itok",
		"token_type":"Bearer",
		"expired":"2026-07-13T12:00:00Z",
		"last_refresh":"2026-07-13T11:00:00Z",
		"email":"u@example.com",
		"sub":"sub1",
		"base_url":"https://api.x.ai/v1",
		"token_endpoint":"https://auth.x.ai/oauth/token"
	}`)
	a, err := store.UpsertFromOAuthJSON(ctx, raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Email != "u@example.com" || a.Subject != "sub1" {
		t.Fatalf("identity: email=%q sub=%q", a.Email, a.Subject)
	}
	if a.ExpiresAt != "2026-07-13T12:00:00Z" || a.LastRefreshAt != "2026-07-13T11:00:00Z" {
		t.Fatalf("times: exp=%q last=%q", a.ExpiresAt, a.LastRefreshAt)
	}
	if a.TokenEndpoint != "https://auth.x.ai/oauth/token" {
		t.Fatalf("token_endpoint=%q", a.TokenEndpoint)
	}
	if a.IDToken != "itok" || a.TokenType != "Bearer" {
		t.Fatalf("id_token/type: %q %q", a.IDToken, a.TokenType)
	}

	out, err := store.ExportJSON(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "xai" {
		t.Fatalf("type=%v", m["type"])
	}
	if m["auth_kind"] != "oauth" {
		t.Fatalf("auth_kind=%v", m["auth_kind"])
	}
	if m["access_token"] != "atok" {
		t.Fatalf("access_token=%v", m["access_token"])
	}
	if m["refresh_token"] != "rtok" {
		t.Fatalf("refresh_token=%v", m["refresh_token"])
	}
	if m["id_token"] != "itok" {
		t.Fatalf("id_token=%v", m["id_token"])
	}
	if m["email"] != "u@example.com" {
		t.Fatalf("email=%v", m["email"])
	}
	if m["sub"] != "sub1" {
		t.Fatalf("sub=%v", m["sub"])
	}
	if m["expired"] != "2026-07-13T12:00:00Z" {
		t.Fatalf("expired=%v", m["expired"])
	}
	if m["last_refresh"] != "2026-07-13T11:00:00Z" {
		t.Fatalf("last_refresh=%v", m["last_refresh"])
	}
	if m["token_endpoint"] != "https://auth.x.ai/oauth/token" {
		t.Fatalf("token_endpoint=%v", m["token_endpoint"])
	}
	if m["base_url"] != "https://api.x.ai/v1" {
		t.Fatalf("base_url=%v", m["base_url"])
	}
}

func TestUpsertRequiresIdentity(t *testing.T) {
	store := openTestStore(t)
	_, err := store.UpsertFromOAuthJSON(context.Background(),
		[]byte(`{"type":"xai","access_token":"a","refresh_token":"r"}`), true)
	if err == nil {
		t.Fatal("expected error when email and sub both empty")
	}
}
