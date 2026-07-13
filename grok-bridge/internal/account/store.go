// Package account provides CRUD, import/export, and listing for Grok OAuth accounts.
package account

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Account is a Grok OAuth account row.
type Account struct {
	ID, Label, Email, Subject           string
	AccessToken, RefreshToken, IDToken  string
	TokenType, ExpiresAt, LastRefreshAt string
	BaseURL, TokenEndpoint              string
	Status, ErrorMessage                string
	Weight                              int
	CreatedAt, UpdatedAt                string
}

// Store persists accounts in SQLite.
type Store struct {
	DB *sql.DB
}

const accountColumns = `
  id, label, email, subject,
  access_token, refresh_token, id_token,
  token_type, expires_at, last_refresh_at,
  base_url, token_endpoint,
  status, error_message, weight,
  created_at, updated_at`

func scanAccount(scanner interface {
	Scan(dest ...any) error
}) (Account, error) {
	var a Account
	err := scanner.Scan(
		&a.ID, &a.Label, &a.Email, &a.Subject,
		&a.AccessToken, &a.RefreshToken, &a.IDToken,
		&a.TokenType, &a.ExpiresAt, &a.LastRefreshAt,
		&a.BaseURL, &a.TokenEndpoint,
		&a.Status, &a.ErrorMessage, &a.Weight,
		&a.CreatedAt, &a.UpdatedAt,
	)
	return a, err
}

// List returns all accounts, newest first.
func (s *Store) List(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT`+accountColumns+`
FROM accounts
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list accounts rows: %w", err)
	}
	if out == nil {
		out = []Account{}
	}
	return out, nil
}

// ListActive returns accounts with status=active, ordered by created_at then id.
func (s *Store) ListActive(ctx context.Context) ([]Account, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT`+accountColumns+`
FROM accounts
WHERE status = 'active'
ORDER BY created_at ASC, id ASC
`)
	if err != nil {
		return nil, fmt.Errorf("list active accounts: %w", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list active accounts rows: %w", err)
	}
	if out == nil {
		out = []Account{}
	}
	return out, nil
}

// Get returns an account by id, or (nil, nil) if not found.
func (s *Store) Get(ctx context.Context, id string) (*Account, error) {
	row := s.DB.QueryRowContext(ctx, `
SELECT`+accountColumns+`
FROM accounts
WHERE id = ?
`, id)
	a, err := scanAccount(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	return &a, nil
}

// SetStatus updates status and error_message for an account.
func (s *Store) SetStatus(ctx context.Context, id, status, errMsg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `
UPDATE accounts
SET status = ?, error_message = ?, updated_at = ?
WHERE id = ?
`, status, errMsg, now, id)
	if err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set status rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("account %q not found", id)
	}
	return nil
}

// SetLabel updates the display label for an account.
func (s *Store) SetLabel(ctx context.Context, id, label string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `
UPDATE accounts
SET label = ?, updated_at = ?
WHERE id = ?
`, label, now, id)
	if err != nil {
		return fmt.Errorf("set label: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set label rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("account %q not found", id)
	}
	return nil
}

// Delete removes an account by id.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete account rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("account %q not found", id)
	}
	return nil
}

// UpdateTokens updates OAuth token fields after a refresh.
func (s *Store) UpdateTokens(ctx context.Context, id string, access, refresh, idToken, expiresAt, lastRefresh string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx, `
UPDATE accounts
SET access_token = ?, refresh_token = ?, id_token = ?,
    expires_at = ?, last_refresh_at = ?, updated_at = ?
WHERE id = ?
`, access, refresh, idToken, expiresAt, lastRefresh, now, id)
	if err != nil {
		return fmt.Errorf("update tokens: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update tokens rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("account %q not found", id)
	}
	return nil
}

// findByEmailOrSubject looks up an existing account by email (preferred) or subject.
func (s *Store) findByEmailOrSubject(ctx context.Context, email, subject string) (*Account, error) {
	if email != "" {
		row := s.DB.QueryRowContext(ctx, `
SELECT`+accountColumns+`
FROM accounts WHERE email = ?
`, email)
		a, err := scanAccount(row)
		if err == nil {
			return &a, nil
		}
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("find by email: %w", err)
		}
	}
	if subject != "" {
		row := s.DB.QueryRowContext(ctx, `
SELECT`+accountColumns+`
FROM accounts WHERE subject = ?
`, subject)
		a, err := scanAccount(row)
		if err == nil {
			return &a, nil
		}
		if err != sql.ErrNoRows {
			return nil, fmt.Errorf("find by subject: %w", err)
		}
	}
	return nil, nil
}

// upsertAccount inserts or updates an account row from parsed fields.
// Returns the account and whether it was newly inserted.
func (s *Store) upsertAccount(ctx context.Context, fields oauthJSON, enable bool) (Account, bool, error) {
	email := fields.Email
	subject := fields.Subject
	if email == "" && subject == "" {
		return Account{}, false, fmt.Errorf("account requires email or sub")
	}

	existing, err := s.findByEmailOrSubject(ctx, email, subject)
	if err != nil {
		return Account{}, false, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	status := "disabled"
	if enable {
		status = "active"
	}

	label := fields.Email
	if label == "" {
		label = fields.Subject
	}

	if existing != nil {
		// Preserve label if already set; clear error on successful re-import when enabling.
		labelToUse := existing.Label
		if labelToUse == "" {
			labelToUse = label
		}
		errMsg := existing.ErrorMessage
		if enable {
			errMsg = ""
		}
		_, err := s.DB.ExecContext(ctx, `
UPDATE accounts SET
  label = ?, email = ?, subject = ?,
  access_token = ?, refresh_token = ?, id_token = ?,
  token_type = ?, expires_at = ?, last_refresh_at = ?,
  base_url = ?, token_endpoint = ?,
  status = ?, error_message = ?, updated_at = ?
WHERE id = ?
`, labelToUse, email, subject,
			fields.AccessToken, fields.RefreshToken, fields.IDToken,
			fields.TokenType, fields.Expire, fields.LastRefresh,
			fields.BaseURL, fields.TokenEndpoint,
			status, errMsg, now, existing.ID)
		if err != nil {
			return Account{}, false, fmt.Errorf("update account: %w", err)
		}
		a, err := s.Get(ctx, existing.ID)
		if err != nil {
			return Account{}, false, err
		}
		if a == nil {
			return Account{}, false, fmt.Errorf("account %q missing after update", existing.ID)
		}
		return *a, false, nil
	}

	id := uuid.NewString()
	_, err = s.DB.ExecContext(ctx, `
INSERT INTO accounts (
  id, label, email, subject,
  access_token, refresh_token, id_token,
  token_type, expires_at, last_refresh_at,
  base_url, token_endpoint,
  status, error_message, weight,
  created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', 1, ?, ?)
`, id, label, email, subject,
		fields.AccessToken, fields.RefreshToken, fields.IDToken,
		fields.TokenType, fields.Expire, fields.LastRefresh,
		fields.BaseURL, fields.TokenEndpoint,
		status, now, now)
	if err != nil {
		return Account{}, false, fmt.Errorf("insert account: %w", err)
	}
	a, err := s.Get(ctx, id)
	if err != nil {
		return Account{}, false, err
	}
	if a == nil {
		return Account{}, false, fmt.Errorf("account %q missing after insert", id)
	}
	return *a, true, nil
}
