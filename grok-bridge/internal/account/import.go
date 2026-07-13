package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
)

// oauthJSON is the CLIProxyAPI-compatible xAI credential shape.
type oauthJSON struct {
	Type          string `json:"type"`
	AuthKind      string `json:"auth_kind"`
	AccessToken   string `json:"access_token"`
	RefreshToken  string `json:"refresh_token"`
	IDToken       string `json:"id_token"`
	TokenType     string `json:"token_type"`
	ExpiresIn     int    `json:"expires_in"`
	Expire        string `json:"expired"`
	LastRefresh   string `json:"last_refresh"`
	Email         string `json:"email"`
	Subject       string `json:"sub"`
	BaseURL       string `json:"base_url"`
	TokenEndpoint string `json:"token_endpoint"`
}

// validateTokenEndpoint rejects non-empty token_endpoint values that fail OAuth SSRF checks.
func validateTokenEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	return xaiauth.ValidateOAuthEndpoint(raw, "token_endpoint")
}

// UpsertFromOAuthJSON imports a single CPA-compatible OAuth JSON object.
// enable sets status to active when true, disabled when false.
// Upsert key: email if non-empty, else subject.
func (s *Store) UpsertFromOAuthJSON(ctx context.Context, raw []byte, enable bool) (Account, error) {
	var fields oauthJSON
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Account{}, fmt.Errorf("parse oauth json: %w", err)
	}
	if fields.Type != "" && fields.Type != "xai" {
		return Account{}, fmt.Errorf("unsupported account type %q (want xai)", fields.Type)
	}
	ep, err := validateTokenEndpoint(fields.TokenEndpoint)
	if err != nil {
		return Account{}, err
	}
	fields.TokenEndpoint = ep
	a, _, err := s.upsertAccount(ctx, fields, enable)
	return a, err
}

// ImportMany accepts a single OAuth JSON object or a JSON array of them.
// Returns how many rows were inserted vs updated.
func (s *Store) ImportMany(ctx context.Context, payload []byte, enable bool) (inserted, updated int, err error) {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return 0, 0, fmt.Errorf("empty import payload")
	}

	var items []json.RawMessage
	if payload[0] == '[' {
		if err := json.Unmarshal(payload, &items); err != nil {
			return 0, 0, fmt.Errorf("parse import array: %w", err)
		}
	} else {
		items = []json.RawMessage{json.RawMessage(payload)}
	}

	for i, raw := range items {
		var fields oauthJSON
		if err := json.Unmarshal(raw, &fields); err != nil {
			return inserted, updated, fmt.Errorf("parse import item %d: %w", i, err)
		}
		if fields.Type != "" && fields.Type != "xai" {
			return inserted, updated, fmt.Errorf("import item %d: unsupported type %q", i, fields.Type)
		}
		ep, err := validateTokenEndpoint(fields.TokenEndpoint)
		if err != nil {
			return inserted, updated, fmt.Errorf("import item %d: %w", i, err)
		}
		fields.TokenEndpoint = ep
		_, isNew, err := s.upsertAccount(ctx, fields, enable)
		if err != nil {
			return inserted, updated, fmt.Errorf("import item %d: %w", i, err)
		}
		if isNew {
			inserted++
		} else {
			updated++
		}
	}
	return inserted, updated, nil
}

// ExportJSON returns a CPA-compatible JSON document for the account.
// Sets type=xai and auth_kind=oauth.
func (s *Store) ExportJSON(ctx context.Context, id string) ([]byte, error) {
	a, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if a == nil {
		return nil, fmt.Errorf("account %q not found", id)
	}
	out := oauthJSON{
		Type:          "xai",
		AuthKind:      "oauth",
		AccessToken:   a.AccessToken,
		RefreshToken:  a.RefreshToken,
		IDToken:       a.IDToken,
		TokenType:     a.TokenType,
		Expire:        a.ExpiresAt,
		LastRefresh:   a.LastRefreshAt,
		Email:         a.Email,
		Subject:       a.Subject,
		BaseURL:       a.BaseURL,
		TokenEndpoint: a.TokenEndpoint,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal export: %w", err)
	}
	return data, nil
}
