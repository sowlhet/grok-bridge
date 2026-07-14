package db_test

import (
	"context"
	"os"
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

func TestOpenChmodsDBFileAndBusyTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "perm.db")
	db, err := dbpkg.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Fatalf("db mode=%o want 0600", mode)
	}
	// busy_timeout should be set (milliseconds).
	var timeout int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout < 5000 {
		t.Fatalf("busy_timeout=%d want >= 5000", timeout)
	}
}

func TestMigrateAddsRequestTimingColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "timing.db")
	db, err := dbpkg.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := dbpkg.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"first_token_seconds", "total_seconds"} {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM pragma_table_info('request_logs') WHERE name = ?`, col).Scan(&name)
		if err != nil {
			t.Fatalf("column %s: %v", col, err)
		}
	}
}
