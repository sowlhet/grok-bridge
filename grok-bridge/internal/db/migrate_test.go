package db_test

import (
	"context"
	"path/filepath"
	"testing"

	dbpkg "github.com/wlhet/grok-bridge/internal/db"
)

func TestMigrateCreatesTables(t *testing.T) {
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
	for _, table := range []string{"accounts", "api_keys", "request_logs", "settings"} {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}
}
