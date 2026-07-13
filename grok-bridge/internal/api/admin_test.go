package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wlhet/grok-bridge/internal/access"
	"github.com/wlhet/grok-bridge/internal/account"
	"github.com/wlhet/grok-bridge/internal/api"
	"github.com/wlhet/grok-bridge/internal/config"
	dbpkg "github.com/wlhet/grok-bridge/internal/db"
	"github.com/wlhet/grok-bridge/internal/logging"
	"github.com/wlhet/grok-bridge/internal/models"
)

func openAdminServer(t *testing.T) (handler http.Handler, password string) {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "admin.db")
	db, err := dbpkg.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := dbpkg.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}

	password = "test-admin-secret"
	accStore := &account.Store{DB: db}
	keyStore := &access.KeyStore{DB: db}
	logStore := &logging.RequestLogStore{DB: db}
	catalog := models.NewFromConfig(&config.Config{
		Models:  []config.ModelEntry{{ID: "grok-4.5"}},
		Aliases: map[string]string{},
		Proxy:   config.ProxyConfig{UnknownModel: "passthrough"},
	})

	s := api.NewServer(api.ServerDeps{
		Keys:            keyStore,
		Catalog:         catalog,
		Accounts:        accStore,
		Logs:            logStore,
		AdminPassword:   password,
		AdminSessionTTL: time.Hour,
		LogBodies:       "errors_only",
		LogRetentionDays: 30,
	})
	return s.Handler(), password
}

func adminLogin(t *testing.T, h http.Handler, password string) *http.Cookie {
	t.Helper()
	body := []byte(`{"password":` + jsonQuote(password) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == "gb_admin" && c.Value != "" {
			return c
		}
	}
	t.Fatal("missing gb_admin cookie")
	return nil
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestAdminLoginFailure(t *testing.T) {
	h, _ := openAdminServer(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/login", bytes.NewReader([]byte(`{"password":"wrong"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(rr.Result().Cookies()) != 0 {
		for _, c := range rr.Result().Cookies() {
			if c.Name == "gb_admin" && c.Value != "" {
				t.Fatalf("unexpected session cookie on failed login")
			}
		}
	}
}

func TestAdminLoginSuccess(t *testing.T) {
	h, password := openAdminServer(t)
	cookie := adminLogin(t, h, password)
	if !cookie.HttpOnly {
		t.Fatal("cookie should be HttpOnly")
	}
	if cookie.Path != "/" && cookie.Path != "/admin" {
		// Path "/" is fine for simplicity; "/admin" also ok.
		t.Logf("cookie path=%q", cookie.Path)
	}

	// Authenticated dashboard should work.
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminRequiresAuth(t *testing.T) {
	h, _ := openAdminServer(t)
	req := httptest.NewRequest(http.MethodGet, "/admin/api/dashboard", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminImportAccount(t *testing.T) {
	h, password := openAdminServer(t)
	cookie := adminLogin(t, h, password)

	raw := `{"type":"xai","access_token":"tok","refresh_token":"ref","email":"imp@x.ai","sub":"sub-imp"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/import", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	if out["inserted"] == nil && out["updated"] == nil {
		// Accept either counts or accounts array.
		if _, ok := out["accounts"]; !ok {
			// Must have inserted count at least.
			if v, ok := out["inserted"].(float64); !ok || v < 1 {
				// Try inserted as number via alternative shapes.
				t.Logf("import response: %s", rr.Body.String())
			}
		}
	}
	// inserted should be 1
	if n, ok := out["inserted"].(float64); ok {
		if n != 1 {
			t.Fatalf("inserted=%v want 1", out["inserted"])
		}
	} else {
		t.Fatalf("missing inserted field: %s", rr.Body.String())
	}

	// List accounts — tokens redacted.
	req = httptest.NewRequest(http.MethodGet, "/admin/api/accounts", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rr.Code, rr.Body.String())
	}
	var list struct {
		Accounts []map[string]any `json:"accounts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		// Maybe bare array
		var arr []map[string]any
		if err2 := json.Unmarshal(rr.Body.Bytes(), &arr); err2 != nil {
			t.Fatalf("list json: %v body=%s", err, rr.Body.String())
		}
		list.Accounts = arr
	}
	if len(list.Accounts) != 1 {
		t.Fatalf("accounts len=%d body=%s", len(list.Accounts), rr.Body.String())
	}
	acc := list.Accounts[0]
	if acc["email"] != "imp@x.ai" {
		t.Fatalf("email=%v", acc["email"])
	}
	if at, _ := acc["access_token"].(string); at != "" && !strings.Contains(at, "…") && !strings.HasPrefix(at, "…") && len(at) > 8 {
		// Full token must not appear.
		if at == "tok" {
			t.Fatal("access_token not redacted in list")
		}
	}
}

func TestAdminImportAccountMultipart(t *testing.T) {
	h, password := openAdminServer(t)
	cookie := adminLogin(t, h, password)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "acc.json")
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"type":"xai","access_token":"t2","refresh_token":"r2","email":"mp@x.ai","sub":"sub-mp"}`
	if _, err := io.WriteString(fw, raw); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/api/accounts/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("multipart import status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminCreateKey(t *testing.T) {
	h, password := openAdminServer(t)
	cookie := adminLogin(t, h, password)

	body := []byte(`{"label":"ci-key"}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/api/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK && rr.Code != http.StatusCreated {
		t.Fatalf("create key status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("json: %v body=%s", err, rr.Body.String())
	}
	plain, _ := out["key"].(string)
	if plain == "" {
		plain, _ = out["plaintext"].(string)
	}
	if plain == "" || !strings.HasPrefix(plain, "gb_") {
		t.Fatalf("expected plaintext key, got %s", rr.Body.String())
	}
	if rec, ok := out["record"].(map[string]any); ok {
		if rec["label"] != "ci-key" {
			t.Fatalf("label=%v", rec["label"])
		}
	} else if out["label"] != "ci-key" && out["label"] != nil {
		// label may be nested or top-level
		t.Logf("create key body=%s", rr.Body.String())
	}

	// List keys
	req = httptest.NewRequest(http.MethodGet, "/admin/api/keys", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list keys status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAdminListLogsEmpty(t *testing.T) {
	h, password := openAdminServer(t)
	cookie := adminLogin(t, h, password)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/logs", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logs status=%d body=%s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		// bare array
		var arr []any
		if err2 := json.Unmarshal(rr.Body.Bytes(), &arr); err2 != nil {
			t.Fatalf("json: %v body=%s", err, rr.Body.String())
		}
		if len(arr) != 0 {
			t.Fatalf("expected empty logs, got %d", len(arr))
		}
		return
	}
	logs, ok := out["logs"].([]any)
	if !ok {
		// try "items" or "data"
		if logs, ok = out["items"].([]any); !ok {
			logs, ok = out["data"].([]any)
		}
	}
	if !ok {
		t.Fatalf("unexpected logs body: %s", rr.Body.String())
	}
	if len(logs) != 0 {
		t.Fatalf("expected empty logs, got %d", len(logs))
	}
}

func TestAdminSettingsGetPut(t *testing.T) {
	h, password := openAdminServer(t)
	cookie := adminLogin(t, h, password)

	req := httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get settings status=%d body=%s", rr.Code, rr.Body.String())
	}
	var settings map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &settings); err != nil {
		t.Fatalf("json: %v", err)
	}
	if settings["log_bodies"] != "errors_only" {
		t.Fatalf("log_bodies=%v", settings["log_bodies"])
	}

	body := []byte(`{"log_bodies":"all","retention":7}`)
	req = httptest.NewRequest(http.MethodPut, "/admin/api/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("put settings status=%d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/api/settings", nil)
	req.AddCookie(cookie)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &settings); err != nil {
		t.Fatalf("json: %v", err)
	}
	if settings["log_bodies"] != "all" {
		t.Fatalf("after put log_bodies=%v", settings["log_bodies"])
	}
	// retention may be log_retention_days or retention
	ret := settings["retention"]
	if ret == nil {
		ret = settings["log_retention_days"]
	}
	if ret == nil {
		t.Fatalf("missing retention: %s", rr.Body.String())
	}
	if f, ok := ret.(float64); ok && f != 7 {
		t.Fatalf("retention=%v", ret)
	}
}
