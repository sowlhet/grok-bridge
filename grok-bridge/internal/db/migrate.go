package db

import (
	"context"
	"database/sql"
	"fmt"
)

// migration is a single schema version step.
type migration struct {
	version int
	sql     string
}

// migrations is the ordered list of schema versions.
// Version numbers are recorded in schema_migrations after successful apply.
var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  email TEXT NOT NULL DEFAULT '',
  subject TEXT NOT NULL DEFAULT '',
  access_token TEXT NOT NULL DEFAULT '',
  refresh_token TEXT NOT NULL DEFAULT '',
  id_token TEXT NOT NULL DEFAULT '',
  token_type TEXT NOT NULL DEFAULT '',
  expires_at TEXT NOT NULL DEFAULT '',
  last_refresh_at TEXT NOT NULL DEFAULT '',
  base_url TEXT NOT NULL DEFAULT '',
  token_endpoint TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'active',
  error_message TEXT NOT NULL DEFAULT '',
  weight INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_email ON accounts(email) WHERE email != '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_subject ON accounts(subject) WHERE subject != '';

CREATE TABLE IF NOT EXISTS api_keys (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  key_prefix TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_used_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS request_logs (
  id TEXT PRIMARY KEY,
  request_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  api_key_id TEXT NOT NULL DEFAULT '',
  api_key_label TEXT NOT NULL DEFAULT '',
  account_id TEXT NOT NULL DEFAULT '',
  account_label TEXT NOT NULL DEFAULT '',
  protocol TEXT NOT NULL DEFAULT '',
  model_requested TEXT NOT NULL DEFAULT '',
  model_upstream TEXT NOT NULL DEFAULT '',
  stream INTEGER NOT NULL DEFAULT 0,
  status_code INTEGER NOT NULL DEFAULT 0,
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  latency_ms INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  client_ip TEXT NOT NULL DEFAULT '',
  user_agent TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  request_body TEXT NOT NULL DEFAULT '',
  response_body TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_request_logs_created ON request_logs(created_at);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`,
	},
}

// Migrate applies pending schema migrations to db.
// It is safe to call multiple times; already-applied versions are skipped.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		var exists int
		err := db.QueryRowContext(ctx,
			`SELECT 1 FROM schema_migrations WHERE version = ?`, m.version,
		).Scan(&exists)
		if err == nil {
			// Already applied.
			continue
		}
		if err != sql.ErrNoRows {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}

		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES (?)`, m.version,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}
