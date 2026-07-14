package api

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wlhet/grok-bridge/internal/account"
	xaiauth "github.com/wlhet/grok-bridge/internal/auth/xai"
	"github.com/wlhet/grok-bridge/internal/logging"
)

const adminCookieName = "gb_admin"

// adminSession is an in-memory admin session entry.
type adminSession struct {
	ExpiresAt time.Time
}

// sessionStore holds random admin session tokens with TTL.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]adminSession
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &sessionStore{
		sessions: make(map[string]adminSession),
		ttl:      ttl,
	}
}

func (ss *sessionStore) create() (token string, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token = hex.EncodeToString(b[:])
	ss.mu.Lock()
	ss.sessions[token] = adminSession{ExpiresAt: time.Now().Add(ss.ttl)}
	ss.mu.Unlock()
	return token, nil
}

func (ss *sessionStore) valid(token string) bool {
	if token == "" {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	sess, ok := ss.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(sess.ExpiresAt) {
		delete(ss.sessions, token)
		return false
	}
	// Sliding TTL on use.
	sess.ExpiresAt = time.Now().Add(ss.ttl)
	ss.sessions[token] = sess
	return true
}

// registerAdminRoutesOn mounts admin login + protected management endpoints on mux.
func (s *Server) registerAdminRoutesOn(mux *http.ServeMux) {
	// Login is public within /admin/api.
	mux.HandleFunc("POST /admin/api/login", s.handleAdminLogin)
	mux.HandleFunc("POST /admin/api/desktop-login", s.handleDesktopLogin)

	// Protected routes.
	mux.HandleFunc("GET /admin/api/dashboard", s.requireAdmin(s.handleAdminDashboard))
	mux.HandleFunc("GET /admin/api/accounts", s.requireAdmin(s.handleAdminListAccounts))
	mux.HandleFunc("POST /admin/api/accounts/import", s.requireAdmin(s.handleAdminImportAccounts))
	mux.HandleFunc("GET /admin/api/accounts/{id}/export", s.requireAdmin(s.handleAdminExportAccount))
	mux.HandleFunc("PATCH /admin/api/accounts/{id}", s.requireAdmin(s.handleAdminPatchAccount))
	mux.HandleFunc("DELETE /admin/api/accounts/{id}", s.requireAdmin(s.handleAdminDeleteAccount))
	mux.HandleFunc("POST /admin/api/accounts/{id}/refresh", s.requireAdmin(s.handleAdminRefreshAccount))
	mux.HandleFunc("POST /admin/api/accounts/oauth/start", s.requireAdmin(s.handleAdminOAuthStart))
	mux.HandleFunc("POST /admin/api/accounts/oauth/poll", s.requireAdmin(s.handleAdminOAuthPoll))
	mux.HandleFunc("GET /admin/api/keys", s.requireAdmin(s.handleAdminListKeys))
	mux.HandleFunc("POST /admin/api/keys", s.requireAdmin(s.handleAdminCreateKey))
	mux.HandleFunc("DELETE /admin/api/keys/{id}", s.requireAdmin(s.handleAdminDeleteKey))
	// Also accept DELETE /admin/api/keys with body {"id":"..."} for plan's GET/POST/DELETE /keys.
	mux.HandleFunc("DELETE /admin/api/keys", s.requireAdmin(s.handleAdminDeleteKeyBody))
	mux.HandleFunc("GET /admin/api/logs", s.requireAdmin(s.handleAdminListLogs))
	mux.HandleFunc("GET /admin/api/logs/{id}", s.requireAdmin(s.handleAdminGetLog))
	mux.HandleFunc("GET /admin/api/settings", s.requireAdmin(s.handleAdminGetSettings))
	mux.HandleFunc("PUT /admin/api/settings", s.requireAdmin(s.handleAdminPutSettings))
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sessions == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "admin not configured"})
			return
		}
		c, err := r.Cookie(adminCookieName)
		if err != nil || c == nil || !s.sessions.valid(c.Value) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	adminPassword := s.adminPassword
	s.mu.Unlock()
	if adminPassword == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "admin password not configured"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	// Constant-time compare; pad to equal length via fixed buffers.
	want := []byte(adminPassword)
	got := []byte(body.Password)
	ok := false
	if len(want) == len(got) {
		ok = subtle.ConstantTimeCompare(want, got) == 1
	} else {
		// Still do a compare against want to avoid trivial timing on length alone
		// for equal-length cases; unequal lengths always fail.
		_ = subtle.ConstantTimeCompare(want, want)
		ok = false
	}
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid password"})
		return
	}
	token, err := s.sessions.create()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to create session"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.sessions.ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDesktopLogin issues an admin session for the desktop shell without password prompt.
// Security constraints:
// - only loopback clients (127.0.0.1 / ::1)
// - requires desktop token header matching server desktopToken
// Browser users still need password login.
func (s *Server) handleDesktopLogin(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "desktop login only on localhost"})
		return
	}
	s.mu.Lock()
	want := s.desktopToken
	s.mu.Unlock()
	if strings.TrimSpace(want) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "desktop login not enabled"})
		return
	}
	got := strings.TrimSpace(r.Header.Get("X-Grok-Bridge-Desktop-Token"))
	if got == "" {
		// also allow JSON body token for flexibility
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		got = strings.TrimSpace(body.Token)
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid desktop token"})
		return
	}
	if s.sessions == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "admin not configured"})
		return
	}
	token, err := s.sessions.create()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to create session"})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.sessions.ttl.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "desktop"})
}

func isLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if host == "" {
		return false
	}
	// RemoteAddr is host:port
	h := host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		// handle [ipv6]:port
		if strings.HasPrefix(host, "[") {
			if end := strings.Index(host, "]"); end > 0 {
				h = host[1:end]
			}
		} else {
			h = host[:i]
		}
	}
	return h == "127.0.0.1" || h == "::1" || h == "localhost"
}

func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "logs not configured"})
		return
	}
	st, err := s.logs.Dashboard(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"today_count":     st.TodayCount,
		"today_errors":    st.TodayErrors,
		"last_7d_count":   st.Last7dCount,
		"last_7d_errors":  st.Last7dErrors,
		"active_accounts": st.ActiveAccounts,
		"top_models":      st.TopModels,
		"top_accounts":    st.TopAccounts,
	})
}

// accountDTO is a redacted account for list/detail responses (no full tokens).
type accountDTO struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Email          string `json:"email"`
	Subject        string `json:"subject"`
	TokenType      string `json:"token_type"`
	ExpiresAt      string `json:"expires_at"`
	LastRefreshAt  string `json:"last_refresh_at"`
	BaseURL        string `json:"base_url"`
	TokenEndpoint  string `json:"token_endpoint"`
	Status         string `json:"status"`
	ErrorMessage   string `json:"error_message"`
	Weight         int    `json:"weight"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	AccessTokenEnd string `json:"access_token_suffix,omitempty"`
	HasRefresh     bool   `json:"has_refresh_token"`
}

func toAccountDTO(a account.Account) accountDTO {
	return accountDTO{
		ID:             a.ID,
		Label:          a.Label,
		Email:          a.Email,
		Subject:        a.Subject,
		TokenType:      a.TokenType,
		ExpiresAt:      a.ExpiresAt,
		LastRefreshAt:  a.LastRefreshAt,
		BaseURL:        a.BaseURL,
		TokenEndpoint:  a.TokenEndpoint,
		Status:         a.Status,
		ErrorMessage:   a.ErrorMessage,
		Weight:         a.Weight,
		CreatedAt:      a.CreatedAt,
		UpdatedAt:      a.UpdatedAt,
		AccessTokenEnd: tokenSuffix(a.AccessToken),
		HasRefresh:     a.RefreshToken != "",
	}
}

func tokenSuffix(tok string) string {
	if tok == "" {
		return ""
	}
	if len(tok) <= 4 {
		return "…"
	}
	return "…" + tok[len(tok)-4:]
}

func (s *Server) handleAdminListAccounts(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	list, err := s.accounts.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	out := make([]accountDTO, 0, len(list))
	for _, a := range list {
		out = append(out, toAccountDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (s *Server) handleAdminImportAccounts(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	payload, err := readImportPayload(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Default enable=true; optional query ?enable=false
	enable := true
	if v := r.URL.Query().Get("enable"); v == "false" || v == "0" {
		enable = false
	}
	inserted, updated, err := s.accounts.ImportMany(r.Context(), payload, enable)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"inserted": inserted,
		"updated":  updated,
	})
}

func readImportPayload(r *http.Request) ([]byte, error) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			return nil, fmt.Errorf("parse multipart: %w", err)
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			return nil, fmt.Errorf("multipart file field required: %w", err)
		}
		defer file.Close()
		return io.ReadAll(io.LimitReader(file, 16<<20))
	}
	return io.ReadAll(io.LimitReader(r.Body, 16<<20))
}

func (s *Server) handleAdminExportAccount(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	id := r.PathValue("id")
	data, err := s.accounts.ExportJSON(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="account-%s.json"`, id))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleAdminPatchAccount(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	id := r.PathValue("id")
	var body struct {
		Status *string `json:"status"`
		Label  *string `json:"label"`
		Weight *int    `json:"weight"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if body.Status != nil {
		st := *body.Status
		switch st {
		case "active", "disabled", "error":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid status"})
			return
		}
		if err := s.accounts.SetStatus(r.Context(), id, st, ""); err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}
	if body.Label != nil {
		if err := s.accounts.SetLabel(r.Context(), id, *body.Label); err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}
	if body.Weight != nil {
		if err := s.accounts.SetWeight(r.Context(), id, *body.Weight); err != nil {
			if strings.Contains(err.Error(), "not found") {
				writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
	}
	a, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if a == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "account not found"})
		return
	}
	writeJSON(w, http.StatusOK, toAccountDTO(*a))
}

func (s *Server) handleAdminDeleteAccount(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	id := r.PathValue("id")
	if err := s.accounts.Delete(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminRefreshAccount(w http.ResponseWriter, r *http.Request) {
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	if s.oauth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "oauth client not configured"})
		return
	}
	id := r.PathValue("id")
	acc, err := s.accounts.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if acc == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "account not found"})
		return
	}
	if strings.TrimSpace(acc.RefreshToken) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "account has no refresh token"})
		return
	}
	td, err := s.oauth.Refresh(r.Context(), acc.TokenEndpoint, acc.RefreshToken)
	if err != nil {
		_ = s.accounts.SetStatus(r.Context(), id, "error", "refresh failed: "+err.Error())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	refresh := td.RefreshToken
	if refresh == "" {
		refresh = acc.RefreshToken
	}
	idToken := td.IDToken
	if idToken == "" {
		idToken = acc.IDToken
	}
	lastRefresh := time.Now().UTC().Format(time.RFC3339)
	if err := s.accounts.UpdateTokens(r.Context(), id, td.AccessToken, refresh, idToken, td.Expire, lastRefresh); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	_ = s.accounts.SetStatus(r.Context(), id, "active", "")
	updated, err := s.accounts.Get(r.Context(), id)
	if err != nil || updated == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	writeJSON(w, http.StatusOK, toAccountDTO(*updated))
}

func (s *Server) handleAdminOAuthStart(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "oauth client not configured"})
		return
	}
	dc, err := s.oauth.StartDeviceFlow(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               dc.DeviceCode,
		"user_code":                 dc.UserCode,
		"verification_uri":          dc.VerificationURI,
		"verification_uri_complete": dc.VerificationURIComplete,
		"expires_in":                dc.ExpiresIn,
		"interval":                  dc.Interval,
		"token_endpoint":            dc.TokenEndpoint,
	})
}

func (s *Server) handleAdminOAuthPoll(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "oauth client not configured"})
		return
	}
	if s.accounts == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accounts not configured"})
		return
	}
	var body struct {
		DeviceCode    string `json:"device_code"`
		TokenEndpoint string `json:"token_endpoint"`
		Enable        *bool  `json:"enable"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(body.DeviceCode) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "device_code required"})
		return
	}
	token, pending, err := s.oauth.PollTokenOnce(r.Context(), body.DeviceCode, body.TokenEndpoint)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if pending {
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	}
	// Build CPA-compatible JSON and upsert.
	enable := true
	if body.Enable != nil {
		enable = *body.Enable
	}
	payload, err := json.Marshal(map[string]any{
		"type":           "xai",
		"auth_kind":      "oauth",
		"access_token":   token.AccessToken,
		"refresh_token":  token.RefreshToken,
		"id_token":       token.IDToken,
		"token_type":     token.TokenType,
		"expires_in":     token.ExpiresIn,
		"expired":        token.Expire,
		"email":          token.Email,
		"sub":            token.Subject,
		"token_endpoint": body.TokenEndpoint,
		"base_url":       xaiauth.CLIChatProxyBaseURL,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	acc, err := s.accounts.UpsertFromOAuthJSON(r.Context(), payload, enable)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "authorized",
		"account": toAccountDTO(acc),
	})
}

func (s *Server) handleAdminListKeys(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "keys not configured"})
		return
	}
	list, err := s.keys.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": list})
}

func (s *Server) handleAdminCreateKey(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "keys not configured"})
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil && err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	plain, rec, err := s.keys.Create(r.Context(), body.Label)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"key":    plain,
		"record": rec,
	})
}

func (s *Server) handleAdminDeleteKey(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "keys not configured"})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id required"})
		return
	}
	if err := s.keys.Revoke(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminDeleteKeyBody(w http.ResponseWriter, r *http.Request) {
	if s.keys == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "keys not configured"})
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if body.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id required"})
		return
	}
	if err := s.keys.Revoke(r.Context(), body.ID); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAdminListLogs(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "logs not configured"})
		return
	}
	q := r.URL.Query()
	f := logging.LogFilter{
		Since:     q.Get("from"),
		Until:     q.Get("to"),
		AccountID: q.Get("account_id"),
		APIKeyID:  q.Get("api_key_id"),
		Model:     q.Get("model"),
		Protocol:  q.Get("protocol"),
	}
	if st := q.Get("status"); st != "" {
		if n, err := strconv.Atoi(st); err == nil {
			f.StatusCode = n
		}
	}
	if lim := q.Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			f.Limit = n
		}
	}
	if off := q.Get("offset"); off != "" {
		if n, err := strconv.Atoi(off); err == nil {
			f.Offset = n
		}
	}
	list, err := s.logs.Query(r.Context(), f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// List without huge bodies by default — strip body fields for list view.
	type logListItem struct {
		ID                string  `json:"id"`
		RequestID         string  `json:"request_id"`
		CreatedAt         string  `json:"created_at"`
		APIKeyID          string  `json:"api_key_id"`
		APIKeyLabel       string  `json:"api_key_label"`
		AccountID         string  `json:"account_id"`
		AccountLabel      string  `json:"account_label"`
		Protocol          string  `json:"protocol"`
		ModelRequested    string  `json:"model_requested"`
		ModelUpstream     string  `json:"model_upstream"`
		Stream            bool    `json:"stream"`
		StatusCode        int     `json:"status_code"`
		ErrorCode         string  `json:"error_code"`
		ErrorMessage      string  `json:"error_message"`
		LatencyMs         int     `json:"latency_ms"`
		FirstTokenSeconds float64 `json:"first_token_seconds"`
		TotalSeconds      float64 `json:"total_seconds"`
		InputTokens       int     `json:"input_tokens"`
		OutputTokens      int     `json:"output_tokens"`
		ClientIP          string  `json:"client_ip"`
		UserAgent         string  `json:"user_agent"`
		Path              string  `json:"path"`
	}
	items := make([]logListItem, 0, len(list))
	for _, rec := range list {
		items = append(items, logListItem{
			ID:                rec.ID,
			RequestID:         rec.RequestID,
			CreatedAt:         rec.CreatedAt,
			APIKeyID:          rec.APIKeyID,
			APIKeyLabel:       rec.APIKeyLabel,
			AccountID:         rec.AccountID,
			AccountLabel:      rec.AccountLabel,
			Protocol:          rec.Protocol,
			ModelRequested:    rec.ModelRequested,
			ModelUpstream:     rec.ModelUpstream,
			Stream:            rec.Stream,
			StatusCode:        rec.StatusCode,
			ErrorCode:         rec.ErrorCode,
			ErrorMessage:      rec.ErrorMessage,
			LatencyMs:         rec.LatencyMs,
			FirstTokenSeconds: rec.FirstTokenSeconds,
			TotalSeconds:      rec.TotalSeconds,
			InputTokens:       rec.InputTokens,
			OutputTokens:      rec.OutputTokens,
			ClientIP:          rec.ClientIP,
			UserAgent:         rec.UserAgent,
			Path:              rec.Path,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": items})
}

func (s *Server) handleAdminGetLog(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "logs not configured"})
		return
	}
	id := r.PathValue("id")
	rec, err := s.logs.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if rec == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "log not found"})
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	logBodies, retention := s.loadRuntimeSettings(r)
	s.mu.Lock()
	listenPort := 18080
	if v, ok := s.loadSettingValue(r, "listen_port"); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 && n <= 65535 {
			listenPort = n
		}
	}
	out := map[string]any{
		"log_bodies":            logBodies,
		"retention":             retention,
		"log_retention_days":    retention,
		"http_proxy":            s.httpProxy,
		"scheduling":            s.scheduling,
		"max_concurrency":       s.maxConcurrency,
		"account_concurrency":   s.accountConcurrency,
		"max_account_switches":  s.maxAccountSwitches,
		"max_transient_retries": s.maxTransientRetries,
		"listen_port":           listenPort,
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminPutSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		LogBodies           *string `json:"log_bodies"`
		Retention           *int    `json:"retention"`
		Retention2          *int    `json:"log_retention_days"`
		AdminPassword       *string `json:"admin_password"`
		HTTPProxy           *string `json:"http_proxy"`
		Scheduling          *string `json:"scheduling"`
		MaxConcurrency      *int    `json:"max_concurrency"`
		AccountConcurrency  *int    `json:"account_concurrency"`
		MaxAccountSwitches  *int    `json:"max_account_switches"`
		MaxTransientRetries *int    `json:"max_transient_retries"`
		ListenPort          *int    `json:"listen_port"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
		return
	}
	if body.ListenPort != nil {
		if *body.ListenPort < 1024 || *body.ListenPort > 65535 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "listen_port must be 1024-65535"})
			return
		}
		_ = s.persistSetting(r, "listen_port", strconv.Itoa(*body.ListenPort))
	}
	if body.LogBodies != nil {
		switch *body.LogBodies {
		case "off", "errors_only", "sample", "all":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid log_bodies"})
			return
		}
		s.mu.Lock()
		s.logBodies = *body.LogBodies
		s.mu.Unlock()
		_ = s.persistSetting(r, "log_bodies", *body.LogBodies)
		if s.pipeline != nil {
			s.pipeline.LogBodies = *body.LogBodies
		}
	}
	ret := body.Retention
	if ret == nil {
		ret = body.Retention2
	}
	if ret != nil {
		if *ret < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid retention"})
			return
		}
		s.mu.Lock()
		s.logRetentionDays = *ret
		s.mu.Unlock()
		_ = s.persistSetting(r, "log_retention_days", strconv.Itoa(*ret))
	}
	if body.HTTPProxy != nil {
		proxy := strings.TrimSpace(*body.HTTPProxy)
		s.mu.Lock()
		s.httpProxy = proxy
		s.mu.Unlock()
		_ = s.persistSetting(r, "http_proxy", proxy)
		if s.onProxySettings != nil {
			s.onProxySettings(ProxySettings{
				HTTPProxy:           proxy,
				Scheduling:          s.scheduling,
				MaxConcurrency:      s.maxConcurrency,
				AccountConcurrency:  s.accountConcurrency,
				MaxAccountSwitches:  s.maxAccountSwitches,
				MaxTransientRetries: s.maxTransientRetries,
			})
		}
	}
	if body.Scheduling != nil {
		mode := strings.TrimSpace(*body.Scheduling)
		switch mode {
		case "round_robin", "weighted":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid scheduling"})
			return
		}
		s.mu.Lock()
		s.scheduling = mode
		s.mu.Unlock()
		_ = s.persistSetting(r, "scheduling", mode)
		if s.onProxySettings != nil {
			s.mu.Lock()
			ps := s.snapshotProxySettingsLocked()
			s.mu.Unlock()
			s.onProxySettings(ps)
		}
	}
	if body.MaxConcurrency != nil {
		if *body.MaxConcurrency < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid max_concurrency"})
			return
		}
		s.mu.Lock()
		s.maxConcurrency = *body.MaxConcurrency
		s.mu.Unlock()
		_ = s.persistSetting(r, "max_concurrency", strconv.Itoa(*body.MaxConcurrency))
		if s.onProxySettings != nil {
			s.mu.Lock()
			ps := s.snapshotProxySettingsLocked()
			s.mu.Unlock()
			s.onProxySettings(ps)
		}
	}
	if body.AccountConcurrency != nil {
		if *body.AccountConcurrency < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid account_concurrency"})
			return
		}
		s.mu.Lock()
		s.accountConcurrency = *body.AccountConcurrency
		s.mu.Unlock()
		_ = s.persistSetting(r, "account_concurrency", strconv.Itoa(*body.AccountConcurrency))
		if s.onProxySettings != nil {
			s.mu.Lock()
			ps := s.snapshotProxySettingsLocked()
			s.mu.Unlock()
			s.onProxySettings(ps)
		}
	}
	if body.MaxAccountSwitches != nil {
		if *body.MaxAccountSwitches < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid max_account_switches"})
			return
		}
		s.mu.Lock()
		s.maxAccountSwitches = *body.MaxAccountSwitches
		s.mu.Unlock()
		_ = s.persistSetting(r, "max_account_switches", strconv.Itoa(*body.MaxAccountSwitches))
		if s.onProxySettings != nil {
			s.mu.Lock()
			ps := s.snapshotProxySettingsLocked()
			s.mu.Unlock()
			s.onProxySettings(ps)
		}
	}
	if body.MaxTransientRetries != nil {
		if *body.MaxTransientRetries < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid max_transient_retries"})
			return
		}
		s.mu.Lock()
		s.maxTransientRetries = *body.MaxTransientRetries
		s.mu.Unlock()
		_ = s.persistSetting(r, "max_transient_retries", strconv.Itoa(*body.MaxTransientRetries))
		if s.onProxySettings != nil {
			s.mu.Lock()
			ps := s.snapshotProxySettingsLocked()
			s.mu.Unlock()
			s.onProxySettings(ps)
		}
	}
	if body.AdminPassword != nil {
		pw := strings.TrimSpace(*body.AdminPassword)
		if pw == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "admin_password must not be empty"})
			return
		}
		if pw == "change-me" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": `admin_password must not be "change-me"`})
			return
		}
		s.mu.Lock()
		s.adminPassword = pw
		s.mu.Unlock()
		// Persist for process restarts that load settings table overlay (yaml still wins at boot unless env set).
		_ = s.persistSetting(r, "admin_password", pw)
	}
	logBodies, retention := s.loadRuntimeSettings(r)
	s.mu.Lock()
	out := map[string]any{
		"log_bodies":            logBodies,
		"retention":             retention,
		"log_retention_days":    retention,
		"admin_password_set":    true,
		"http_proxy":            s.httpProxy,
		"scheduling":            s.scheduling,
		"max_concurrency":       s.maxConcurrency,
		"account_concurrency":   s.accountConcurrency,
		"max_account_switches":  s.maxAccountSwitches,
		"max_transient_retries": s.maxTransientRetries,
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) loadRuntimeSettings(r *http.Request) (logBodies string, retention int) {
	s.mu.Lock()
	logBodies = s.logBodies
	retention = s.logRetentionDays
	s.mu.Unlock()
	if logBodies == "" {
		logBodies = "errors_only"
	}
	if retention == 0 {
		retention = 30
	}
	// Overlay from settings table if present.
	if db := s.settingsDB(); db != nil {
		var v string
		if err := db.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key = ?`, "log_bodies").Scan(&v); err == nil && v != "" {
			logBodies = v
		}
		if err := db.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key = ?`, "log_retention_days").Scan(&v); err == nil && v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				retention = n
			}
		}
	}
	return logBodies, retention
}

func (s *Server) persistSetting(r *http.Request, key, value string) error {
	db := s.settingsDB()
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(r.Context(), `
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
`, key, value)
	return err
}

func (s *Server) settingsDB() *sql.DB {
	if s.accounts != nil && s.accounts.DB != nil {
		return s.accounts.DB
	}
	if s.logs != nil && s.logs.DB != nil {
		return s.logs.DB
	}
	if s.keys != nil && s.keys.DB != nil {
		return s.keys.DB
	}
	return nil
}


func (s *Server) loadSettingValue(r *http.Request, key string) (string, bool) {
	db := s.dbFrom(r)
	if db == nil {
		return "", false
	}
	var v string
	if err := db.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}


func (s *Server) dbFrom(r *http.Request) *sql.DB {
	_ = r
	if s.logs != nil && s.logs.DB != nil {
		return s.logs.DB
	}
	if s.accounts != nil && s.accounts.DB != nil {
		return s.accounts.DB
	}
	return nil
}
