// Package access manages client API keys (create, verify, list, revoke).
// Plaintext keys are never stored; only SHA-256 hashes are persisted.
package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// KeyRecord is a client API key metadata row (no plaintext).
type KeyRecord struct {
	ID         string
	Label      string
	KeyPrefix  string
	Enabled    bool
	LastUsedAt string
	CreatedAt  string
}

// KeyStore persists and verifies client API keys in SQLite.
type KeyStore struct {
	DB *sql.DB
}

// HashKey returns the SHA-256 hex digest of plaintext.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// NewPlaintextKey generates a new random API key: "gb_" + 32 random bytes hex.
func NewPlaintextKey() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate api key: %w", err)
	}
	return "gb_" + hex.EncodeToString(b[:]), nil
}

// Create generates a new API key, stores its hash, and returns the plaintext once.
func (s *KeyStore) Create(ctx context.Context, label string) (plaintext string, rec KeyRecord, err error) {
	plain, err := NewPlaintextKey()
	if err != nil {
		return "", KeyRecord{}, err
	}

	id := uuid.NewString()
	// Display prefix: "gb_" + first 8 hex chars of the random part (11 chars total).
	prefix := plain
	if len(prefix) > 11 {
		prefix = prefix[:11]
	}
	hash := HashKey(plain)
	createdAt := time.Now().UTC().Format(time.RFC3339)

	_, err = s.DB.ExecContext(ctx, `
INSERT INTO api_keys (id, label, key_prefix, key_hash, enabled, last_used_at, created_at)
VALUES (?, ?, ?, ?, 1, '', ?)
`, id, label, prefix, hash, createdAt)
	if err != nil {
		return "", KeyRecord{}, fmt.Errorf("insert api_key: %w", err)
	}

	rec = KeyRecord{
		ID:         id,
		Label:      label,
		KeyPrefix:  prefix,
		Enabled:    true,
		LastUsedAt: "",
		CreatedAt:  createdAt,
	}
	return plain, rec, nil
}

// Verify looks up a plaintext key by hash.
// Returns (nil, nil) when the key is missing or disabled.
// On successful verify, last_used_at is updated.
func (s *KeyStore) Verify(ctx context.Context, plaintext string) (*KeyRecord, error) {
	if plaintext == "" {
		return nil, nil
	}
	hash := HashKey(plaintext)

	var rec KeyRecord
	var enabled int
	err := s.DB.QueryRowContext(ctx, `
SELECT id, label, key_prefix, enabled, last_used_at, created_at
FROM api_keys
WHERE key_hash = ?
`, hash).Scan(&rec.ID, &rec.Label, &rec.KeyPrefix, &enabled, &rec.LastUsedAt, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("verify api_key: %w", err)
	}
	if enabled == 0 {
		return nil, nil
	}
	rec.Enabled = true

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ? WHERE id = ?`, now, rec.ID,
	); err != nil {
		return nil, fmt.Errorf("update last_used_at: %w", err)
	}
	rec.LastUsedAt = now
	return &rec, nil
}

// List returns all API keys (enabled and revoked), newest first.
func (s *KeyStore) List(ctx context.Context) ([]KeyRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT id, label, key_prefix, enabled, last_used_at, created_at
FROM api_keys
ORDER BY created_at DESC, id DESC
`)
	if err != nil {
		return nil, fmt.Errorf("list api_keys: %w", err)
	}
	defer rows.Close()

	var out []KeyRecord
	for rows.Next() {
		var rec KeyRecord
		var enabled int
		if err := rows.Scan(&rec.ID, &rec.Label, &rec.KeyPrefix, &enabled, &rec.LastUsedAt, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan api_key: %w", err)
		}
		rec.Enabled = enabled != 0
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list api_keys rows: %w", err)
	}
	if out == nil {
		out = []KeyRecord{}
	}
	return out, nil
}

// Revoke disables an API key by id (soft revoke).
func (s *KeyStore) Revoke(ctx context.Context, id string) error {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE api_keys SET enabled = 0 WHERE id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("revoke api_key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke api_key rows: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("api key %q not found", id)
	}
	return nil
}
