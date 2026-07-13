// Package db opens the SQLite database and runs schema migrations.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens (or creates) a SQLite database at path.
// The parent directory is created with mode 0700 if it does not exist.
// After open, the DB file is chmod'd to 0600 and busy_timeout is set to 5s.
func Open(path string) (*sql.DB, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create db dir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// Verify the connection is usable (also creates the file if missing).
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %s: %w", path, err)
	}
	// Restrict file permissions after the file exists.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("chmod sqlite %s: %w", path, err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	return db, nil
}
