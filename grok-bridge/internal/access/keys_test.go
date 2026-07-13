package access_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wlhet/grok-bridge/internal/access"
	dbpkg "github.com/wlhet/grok-bridge/internal/db"
)

func openTestStore(t *testing.T) *access.KeyStore {
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
	return &access.KeyStore{DB: db}
}

func TestCreateAndVerify(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	plain, rec, err := store.Create(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "gb_") {
		t.Fatalf("prefix %q", plain)
	}
	if rec.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if rec.Label != "test" {
		t.Fatalf("label=%q", rec.Label)
	}
	if !rec.Enabled {
		t.Fatal("expected enabled key")
	}
	if rec.KeyPrefix == "" {
		t.Fatal("expected key_prefix")
	}
	if !strings.HasPrefix(plain, rec.KeyPrefix) {
		t.Fatalf("prefix %q not prefix of plain %q", rec.KeyPrefix, plain)
	}

	got, err := store.Verify(ctx, plain)
	if err != nil || got == nil || got.ID != rec.ID {
		t.Fatalf("verify failed: got=%v err=%v", got, err)
	}
	if got.Label != "test" {
		t.Fatalf("verify label=%q", got.Label)
	}

	bad, err := store.Verify(ctx, "gb_nope")
	if err != nil || bad != nil {
		t.Fatalf("expected nil for bad key, got=%v err=%v", bad, err)
	}
}

func TestCreateDoesNotStorePlaintext(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	plain, rec, err := store.Create(ctx, "secret-key")
	if err != nil {
		t.Fatal(err)
	}

	var keyHash, keyPrefix string
	err = store.DB.QueryRowContext(ctx,
		`SELECT key_hash, key_prefix FROM api_keys WHERE id = ?`, rec.ID,
	).Scan(&keyHash, &keyPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if keyHash == plain {
		t.Fatal("plaintext stored as key_hash")
	}
	if keyHash != access.HashKey(plain) {
		t.Fatalf("hash mismatch: db=%q want=%q", keyHash, access.HashKey(plain))
	}
	if strings.Contains(keyHash, plain) {
		t.Fatal("plaintext appears inside key_hash")
	}

	// Scan all text columns to ensure plaintext is never persisted.
	rows, err := store.DB.QueryContext(ctx, `SELECT id, label, key_prefix, key_hash, last_used_at, created_at FROM api_keys`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, label, prefix, hash, lastUsed, created string
		if err := rows.Scan(&id, &label, &prefix, &hash, &lastUsed, &created); err != nil {
			t.Fatal(err)
		}
		for _, col := range []string{id, label, prefix, hash, lastUsed, created} {
			if col == plain {
				t.Fatalf("plaintext key found in stored column value %q", col)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestListAndRevoke(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	plain1, rec1, err := store.Create(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	plain2, rec2, err := store.Create(ctx, "two")
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d want 2", len(list))
	}
	ids := map[string]bool{}
	for _, k := range list {
		ids[k.ID] = true
		if !k.Enabled {
			t.Fatalf("key %s should be enabled", k.ID)
		}
	}
	if !ids[rec1.ID] || !ids[rec2.ID] {
		t.Fatalf("list missing ids: %+v", ids)
	}

	if err := store.Revoke(ctx, rec1.ID); err != nil {
		t.Fatal(err)
	}

	// Revoked key must not verify.
	got, err := store.Verify(ctx, plain1)
	if err != nil || got != nil {
		t.Fatalf("revoked key still verifies: got=%v err=%v", got, err)
	}

	// Other key still works.
	got2, err := store.Verify(ctx, plain2)
	if err != nil || got2 == nil || got2.ID != rec2.ID {
		t.Fatalf("other key verify failed: got=%v err=%v", got2, err)
	}

	list, err = store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range list {
		if k.ID == rec1.ID && k.Enabled {
			t.Fatal("revoked key still enabled in list")
		}
		if k.ID == rec2.ID && !k.Enabled {
			t.Fatal("active key disabled in list")
		}
	}
}

func TestHashKeyAndNewPlaintextKey(t *testing.T) {
	k1, err := access.NewPlaintextKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := access.NewPlaintextKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k1, "gb_") || !strings.HasPrefix(k2, "gb_") {
		t.Fatalf("keys missing prefix: %q %q", k1, k2)
	}
	// "gb_" + 64 hex chars (32 bytes)
	if len(k1) != 3+64 {
		t.Fatalf("key length=%d want %d", len(k1), 3+64)
	}
	if k1 == k2 {
		t.Fatal("NewPlaintextKey returned duplicate keys")
	}

	h1 := access.HashKey(k1)
	h2 := access.HashKey(k1)
	if h1 != h2 {
		t.Fatal("HashKey not deterministic")
	}
	if len(h1) != 64 {
		t.Fatalf("sha256 hex length=%d want 64", len(h1))
	}
	if access.HashKey(k1) == access.HashKey(k2) {
		t.Fatal("different keys hashed equal")
	}
}

func TestRevokeMissingID(t *testing.T) {
	store := openTestStore(t)
	err := store.Revoke(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestVerifyDisabledKey(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	plain, rec, err := store.Create(ctx, "to-disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.Verify(ctx, plain)
	if err != nil || got != nil {
		t.Fatalf("disabled key should be invalid: got=%v err=%v", got, err)
	}
}
