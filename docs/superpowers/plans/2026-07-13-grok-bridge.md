# Grok Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a standalone Go proxy (`grok-bridge`) that lets Claude Code and Codex call xAI Grok Build via OAuth, with client API keys, multi-account round-robin, JSON account import, request logs, and an embedded Web admin.

**Architecture:** Single binary HTTP server. Public routes require hashed client API keys; admin routes use a separate password session. Requests are translated (Claude Messages / OpenAI ChatCompletions / Responses → xAI Responses), executed against a pooled Grok OAuth account, streamed or JSON-returned, and logged to SQLite. Admin UI is static assets embedded with `go:embed`.

**Tech Stack:** Go 1.26+, `net/http` (stdlib router via `http.ServeMux` Go 1.22+ patterns), `modernc.org/sqlite` (pure Go SQLite), `gopkg.in/yaml.v3`, `github.com/google/uuid`, `golang.org/x/crypto` (bcrypt for admin password optional; API keys use SHA-256), lightweight admin HTML/JS (no build step for v1: vanilla JS SPA in `internal/adminui/static`).

**Spec:** `docs/superpowers/specs/2026-07-13-grok-bridge-design.md`  
**Reference (read-only):** `CLIProxyAPI/internal/auth/xai/*`, `CLIProxyAPI/internal/runtime/executor/xai_executor.go`

## Global Constraints

- Project root for the app: `grok-bridge/` under the workspace (`/Users/wlhet/tokens/grok-bridge`)
- Comments and user-facing admin UI strings: English in code comments; admin UI may be Chinese if preferred later — **default English for v1**
- Do not vendor or import CLIProxyAPI as a module; reimplement needed bits
- OAuth constants must match CPA/xAI CLI:
  - `DiscoveryURL = https://auth.x.ai/.well-known/openid-configuration`
  - `ClientID = b1a00492-073a-47ea-816f-4c329264a828`
  - `Scope = openid profile email offline_access grok-cli:access api:access`
  - `DefaultAPIBaseURL = https://api.x.ai/v1`
  - `CLIChatProxyBaseURL = https://cli-chat-proxy.grok.com/v1`
  - Device grant: `urn:ietf:params:oauth:grant-type:device_code`
- No image/video/WebSocket/other providers in v1
- TDD: write failing test → implement → pass → commit per task
- After Go edits: `gofmt -w` on touched files; `go test ./...` must pass before commit
- Commits: conventional style (`feat:`, `test:`, `docs:`, `chore:`)

## File Structure (target)

```
grok-bridge/
  go.mod
  go.sum
  config.example.yaml
  Dockerfile
  docker-compose.yml
  README.md
  cmd/
    server/main.go
    login/main.go
  internal/
    config/config.go
    config/config_test.go
    db/db.go
    db/migrate.go
    db/migrate_test.go
    access/keys.go
    access/keys_test.go
    auth/xai/types.go
    auth/xai/oauth.go
    auth/xai/oauth_test.go
    account/store.go
    account/import.go
    account/picker.go
    account/store_test.go
    account/import_test.go
    account/picker_test.go
    executor/xai/client.go
    executor/xai/client_test.go
    translate/types.go
    translate/openai_chat.go
    translate/openai_responses.go
    translate/claude.go
    translate/claude_test.go
    translate/openai_test.go
    thinking/map.go
    thinking/map_test.go
    models/catalog.go
    models/catalog_test.go
    logging/store.go
    logging/store_test.go
    pipeline/pipeline.go
    pipeline/pipeline_test.go
    api/server.go
    api/middleware.go
    api/public.go
    api/public_test.go
    api/admin.go
    api/admin_test.go
    adminui/embed.go
    adminui/static/index.html
    adminui/static/app.js
    adminui/static/styles.css
```

---

### Task 1: Project skeleton and health endpoint

**Files:**
- Create: `grok-bridge/go.mod`
- Create: `grok-bridge/cmd/server/main.go`
- Create: `grok-bridge/internal/api/server.go`
- Create: `grok-bridge/internal/api/public_test.go`
- Create: `grok-bridge/config.example.yaml`
- Create: `grok-bridge/README.md` (minimal stub)

**Interfaces:**
- Produces: `api.NewServer(cfg) *Server` with `Handler() http.Handler` and `ListenAndServe(addr string) error`
- Produces: `GET /healthz` → `200` body `ok`

- [ ] **Step 1: Init module**

```bash
mkdir -p grok-bridge/cmd/server grok-bridge/internal/api
cd grok-bridge
go mod init github.com/wlhet/grok-bridge
```

- [ ] **Step 2: Write failing health test**

```go
// internal/api/public_test.go
package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wlhet/grok-bridge/internal/api"
)

func TestHealthz(t *testing.T) {
	s := api.NewServer(api.ServerDeps{})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body=%q", rr.Body.String())
	}
}
```

- [ ] **Step 3: Run test — expect fail**

```bash
cd grok-bridge && go test ./internal/api/ -run TestHealthz -v
```

Expected: fail (package/types missing)

- [ ] **Step 4: Minimal server**

```go
// internal/api/server.go
package api

import "net/http"

type ServerDeps struct{}

type Server struct {
	mux *http.ServeMux
}

func NewServer(deps ServerDeps) *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }
```

```go
// cmd/server/main.go
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/wlhet/grok-bridge/internal/api"
)

func main() {
	addr := ":8080"
	if v := os.Getenv("GROK_BRIDGE_LISTEN"); v != "" {
		addr = v
	}
	s := api.NewServer(api.ServerDeps{})
	log.Printf("listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, s.Handler()))
}
```

- [ ] **Step 5: Pass tests and commit**

```bash
cd grok-bridge && go test ./... && go build -o /tmp/grok-bridge ./cmd/server
git add grok-bridge && git commit -m "feat: scaffold grok-bridge with healthz"
```

---

### Task 2: Config load (YAML + env)

**Files:**
- Create: `grok-bridge/internal/config/config.go`
- Create: `grok-bridge/internal/config/config_test.go`
- Create: `grok-bridge/config.example.yaml`
- Modify: `grok-bridge/cmd/server/main.go` to load config

**Interfaces:**
- Produces:
  ```go
  type Config struct {
      Server ServerConfig `yaml:"server"`
      Admin  AdminConfig  `yaml:"admin"`
      Data   DataConfig   `yaml:"data"`
      Proxy  ProxyConfig  `yaml:"proxy"`
      Models []ModelEntry `yaml:"models"`
      Aliases map[string]string `yaml:"aliases"`
      XAI    XAIConfig    `yaml:"xai"`
  }
  func Load(path string) (*Config, error)
  func (c *Config) ApplyEnv()
  ```

- [ ] **Step 1: Failing test for defaults + YAML**

```go
func TestLoadMinimalYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte("server:\n  listen: \"127.0.0.1:9090\"\nadmin:\n  password: \"secret\"\ndata:\n  sqlite_path: \"./data/test.db\"\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Listen != "127.0.0.1:9090" {
		t.Fatalf("listen=%q", cfg.Server.Listen)
	}
	if cfg.Admin.Password != "secret" {
		t.Fatalf("password")
	}
	if cfg.Proxy.LogBodies == "" {
		t.Fatal("expected default log_bodies")
	}
}
```

- [ ] **Step 2: Run — fail**

```bash
cd grok-bridge && go test ./internal/config/ -v
```

- [ ] **Step 3: Implement `Load` with defaults**

Defaults:
- `server.listen`: `0.0.0.0:8080`
- `proxy.log_bodies`: `errors_only`
- `proxy.log_retention_days`: `30`
- `proxy.retry.max_account_switches`: `2`
- `proxy.retry.max_transient_retries`: `2`
- `proxy.unknown_model`: `passthrough`
- `admin.session_ttl`: `24h`
- built-in models if empty: `grok-4.5`, `grok-4.3`, `grok-3-mini`

Env overrides in `ApplyEnv`:
- `GROK_BRIDGE_LISTEN` → `Server.Listen`
- `GROK_BRIDGE_ADMIN_PASSWORD` → `Admin.Password`
- `GROK_BRIDGE_SQLITE_PATH` → `Data.SQLitePath`

Dependency: `go get gopkg.in/yaml.v3`

- [ ] **Step 4: Pass + write `config.example.yaml` matching design §10**

- [ ] **Step 5: Commit**

```bash
git add grok-bridge/internal/config grok-bridge/config.example.yaml grok-bridge/cmd/server/main.go grok-bridge/go.mod grok-bridge/go.sum
git commit -m "feat: load yaml config with env overrides"
```

---

### Task 3: SQLite open + migrations

**Files:**
- Create: `grok-bridge/internal/db/db.go`
- Create: `grok-bridge/internal/db/migrate.go`
- Create: `grok-bridge/internal/db/migrate_test.go`

**Interfaces:**
- Produces: `func Open(path string) (*sql.DB, error)`
- Produces: `func Migrate(ctx context.Context, db *sql.DB) error`
- Tables: `schema_migrations`, `accounts`, `api_keys`, `request_logs`, `settings`

- [ ] **Step 1: Failing migration test**

```go
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
```

- [ ] **Step 2: Run — fail**

```bash
cd grok-bridge && go get modernc.org/sqlite && go test ./internal/db/ -v
```

- [ ] **Step 3: Implement Open + Migrate**

`accounts` columns (exact):
```sql
CREATE TABLE accounts (
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
CREATE UNIQUE INDEX idx_accounts_email ON accounts(email) WHERE email != '';
CREATE UNIQUE INDEX idx_accounts_subject ON accounts(subject) WHERE subject != '';
```

`api_keys`:
```sql
CREATE TABLE api_keys (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL DEFAULT '',
  key_prefix TEXT NOT NULL,
  key_hash TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_used_at TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
```

`request_logs`:
```sql
CREATE TABLE request_logs (
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
CREATE INDEX idx_request_logs_created ON request_logs(created_at);
```

`settings`:
```sql
CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

Use `modernc.org/sqlite` driver name `sqlite`. Ensure parent dir of path is created with `0700`.

- [ ] **Step 4: Pass + commit**

```bash
git commit -m "feat: sqlite open and schema migrations"
```

---

### Task 4: Client API key access control

**Files:**
- Create: `grok-bridge/internal/access/keys.go`
- Create: `grok-bridge/internal/access/keys_test.go`

**Interfaces:**
```go
type KeyStore struct { DB *sql.DB }
func (s *KeyStore) Create(ctx context.Context, label string) (plaintext string, rec KeyRecord, err error)
func (s *KeyStore) Verify(ctx context.Context, plaintext string) (*KeyRecord, error) // nil,nil if invalid
func (s *KeyStore) List(ctx context.Context) ([]KeyRecord, error)
func (s *KeyStore) Revoke(ctx context.Context, id string) error
func HashKey(plaintext string) string // sha256 hex
func NewPlaintextKey() (string, error) // "gb_" + 32 random bytes hex
```

- [ ] **Step 1: Tests**

```go
func TestCreateAndVerify(t *testing.T) {
	// open temp db, migrate
	store := &access.KeyStore{DB: db}
	plain, rec, err := store.Create(context.Background(), "test")
	if err != nil { t.Fatal(err) }
	if !strings.HasPrefix(plain, "gb_") { t.Fatalf("prefix %q", plain) }
	got, err := store.Verify(context.Background(), plain)
	if err != nil || got == nil || got.ID != rec.ID { t.Fatalf("verify failed") }
	bad, err := store.Verify(context.Background(), "gb_nope")
	if err != nil || bad != nil { t.Fatalf("expected nil for bad key") }
}
```

- [ ] **Step 2: Implement with SHA-256 hash; never store plaintext**

- [ ] **Step 3: Pass + commit**

```bash
git commit -m "feat: client API key create/verify/revoke"
```

---

### Task 5: Account store + CPA JSON import/export

**Files:**
- Create: `grok-bridge/internal/account/store.go`
- Create: `grok-bridge/internal/account/import.go`
- Create: `grok-bridge/internal/account/store_test.go`
- Create: `grok-bridge/internal/account/import_test.go`

**Interfaces:**
```go
type Account struct {
    ID, Label, Email, Subject string
    AccessToken, RefreshToken, IDToken string
    TokenType, ExpiresAt, LastRefreshAt string
    BaseURL, TokenEndpoint string
    Status, ErrorMessage string
    Weight int
    CreatedAt, UpdatedAt string
}
type Store struct { DB *sql.DB }
func (s *Store) UpsertFromOAuthJSON(ctx context.Context, raw []byte, enable bool) (Account, error)
func (s *Store) ImportMany(ctx context.Context, payload []byte, enable bool) (inserted, updated int, err error)
func (s *Store) List(ctx context.Context) ([]Account, error)
func (s *Store) Get(ctx context.Context, id string) (*Account, error)
func (s *Store) SetStatus(ctx context.Context, id, status, errMsg string) error
func (s *Store) Delete(ctx context.Context, id string) error
func (s *Store) ExportJSON(ctx context.Context, id string) ([]byte, error) // CPA-compatible
func (s *Store) ListActive(ctx context.Context) ([]Account, error)
func (s *Store) UpdateTokens(ctx context.Context, id string, access, refresh, idToken, expiresAt, lastRefresh string) error
```

CPA JSON shape (must accept):
```json
{
  "type": "xai",
  "auth_kind": "oauth",
  "access_token": "a",
  "refresh_token": "r",
  "email": "u@example.com",
  "sub": "sub1",
  "expired": "2026-07-13T12:00:00Z",
  "token_endpoint": "https://auth.x.ai/oauth/token"
}
```

Import rules:
- Accept single object or JSON array
- Upsert by email if non-empty else subject
- Export sets `type=xai`, `auth_kind=oauth`

- [ ] **Step 1: Tests for import upsert + export round-trip**

```go
func TestImportUpsertByEmail(t *testing.T) {
	raw := []byte(`{"type":"xai","access_token":"a1","refresh_token":"r1","email":"a@x.ai","sub":"s1"}`)
	store := &account.Store{DB: db}
	a1, err := store.UpsertFromOAuthJSON(context.Background(), raw, true)
	if err != nil { t.Fatal(err) }
	raw2 := []byte(`{"type":"xai","access_token":"a2","refresh_token":"r2","email":"a@x.ai","sub":"s1"}`)
	a2, err := store.UpsertFromOAuthJSON(context.Background(), raw2, true)
	if err != nil { t.Fatal(err) }
	if a1.ID != a2.ID { t.Fatal("expected same id") }
	if a2.AccessToken != "a2" { t.Fatal("token not updated") }
	out, err := store.ExportJSON(context.Background(), a2.ID)
	if err != nil { t.Fatal(err) }
	if !bytes.Contains(out, []byte(`"type": "xai"`)) && !bytes.Contains(out, []byte(`"type":"xai"`)) {
		t.Fatalf("export missing type: %s", out)
	}
}
```

- [ ] **Step 2: Implement + pass + commit**

```bash
git commit -m "feat: account store with CPA JSON import/export"
```

---

### Task 6: xAI OAuth device flow + refresh

**Files:**
- Create: `grok-bridge/internal/auth/xai/types.go`
- Create: `grok-bridge/internal/auth/xai/oauth.go`
- Create: `grok-bridge/internal/auth/xai/oauth_test.go`

**Interfaces:**
```go
const (
  DefaultAPIBaseURL = "https://api.x.ai/v1"
  CLIChatProxyBaseURL = "https://cli-chat-proxy.grok.com/v1"
  DiscoveryURL = "https://auth.x.ai/.well-known/openid-configuration"
  ClientID = "b1a00492-073a-47ea-816f-4c329264a828"
  Scope = "openid profile email offline_access grok-cli:access api:access"
  DeviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"
)
type Client struct { HTTP *http.Client }
func (c *Client) Discover(ctx context.Context) (*Discovery, error)
func (c *Client) StartDeviceFlow(ctx context.Context) (*DeviceCodeResponse, error)
func (c *Client) PollToken(ctx context.Context, dc *DeviceCodeResponse) (*TokenData, error)
func (c *Client) Refresh(ctx context.Context, tokenEndpoint, refreshToken string) (*TokenData, error)
func ValidateOAuthEndpoint(rawURL, field string) (string, error) // https + host x.ai or *.x.ai
```

- [ ] **Step 1: Unit tests with `httptest.Server` mocking discovery, device, token endpoints**

Cover:
- Discover parses endpoints
- ValidateOAuthEndpoint rejects non-https / wrong host
- Refresh posts `grant_type=refresh_token` + client_id
- Device poll handles `authorization_pending` then success

- [ ] **Step 2: Implement following CPA `internal/auth/xai/xai.go` logic (simplified, same constants)**

- [ ] **Step 3: Wire `cmd/login/main.go`**: load config → open db → device flow → print URL/code → poll → Upsert account

- [ ] **Step 4: Pass + commit**

```bash
git commit -m "feat: xAI OAuth device flow and token refresh"
```

---

### Task 7: Account picker (round-robin + skip unhealthy)

**Files:**
- Create: `grok-bridge/internal/account/picker.go`
- Create: `grok-bridge/internal/account/picker_test.go`

**Interfaces:**
```go
type Picker struct {
    Store *Store
    mu sync.Mutex
    rr uint64
}
func (p *Picker) Next(ctx context.Context) (*Account, error) // err if none active
func (p *Picker) MarkError(ctx context.Context, id, msg string) error
func (p *Picker) MarkActive(ctx context.Context, id string) error
```

- [ ] **Step 1: Test round-robin across 3 active accounts; disabled skipped**

```go
func TestPickerRoundRobin(t *testing.T) {
	// insert three active accounts A B C, one disabled D
	p := &account.Picker{Store: store}
	var ids []string
	for i := 0; i < 3; i++ {
		a, err := p.Next(context.Background())
		if err != nil { t.Fatal(err) }
		ids = append(ids, a.ID)
	}
	if ids[0] == ids[1] && ids[1] == ids[2] {
		t.Fatal("expected rotation")
	}
}
```

- [ ] **Step 2: Implement + commit**

```bash
git commit -m "feat: round-robin account picker"
```

---

### Task 8: Model catalog + aliases

**Files:**
- Create: `grok-bridge/internal/models/catalog.go`
- Create: `grok-bridge/internal/models/catalog_test.go`

**Interfaces:**
```go
type Catalog struct {
    Models  []string
    Aliases map[string]string
    Unknown string // "strict" | "passthrough"
}
func (c *Catalog) Resolve(requested string) (upstream string, err error)
func (c *Catalog) ListOpenAI() any  // {"object":"list","data":[...]}
func (c *Catalog) ListClaude() any  // anthropic-ish list
```

- [ ] **Step 1: Tests for alias resolution and strict mode rejection**

- [ ] **Step 2: Implement from config Models/Aliases**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: model catalog and aliases"
```

---

### Task 9: Thinking / reasoning field mapping

**Files:**
- Create: `grok-bridge/internal/thinking/map.go`
- Create: `grok-bridge/internal/thinking/map_test.go`

**Interfaces:**
```go
// ApplyClaudeToXAI maps Anthropic thinking config into xAI request JSON (gjson/sjson or encoding/json)
func ApplyClaudeToXAI(req map[string]any) map[string]any
// ApplyOpenAIToXAI maps reasoning effort / include fields if present
func ApplyOpenAIToXAI(req map[string]any) map[string]any
// ExtractReasoningFromXAIEvent converts xAI reasoning SSE pieces for Claude thinking blocks / OpenAI reasoning
```

- [ ] **Step 1: Table-driven tests with sample JSON fixtures** (minimal: presence of reasoning fields; strip if disabled)

- [ ] **Step 2: Implement practical subset** (do not copy entire CPA thinking package; enough for Claude Code + Codex)

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: thinking/reasoning field mapping helpers"
```

---

### Task 10: Translators — OpenAI chat + Responses ↔ xAI Responses

**Files:**
- Create: `grok-bridge/internal/translate/types.go`
- Create: `grok-bridge/internal/translate/openai_chat.go`
- Create: `grok-bridge/internal/translate/openai_responses.go`
- Create: `grok-bridge/internal/translate/openai_test.go`

**Interfaces:**
```go
type Direction string
const (
  FormatClaude Format = "claude"
  FormatOpenAIChat Format = "openai_chat"
  FormatOpenAIResponses Format = "openai_responses"
  FormatXAI Format = "xai"
)
func ChatCompletionsToXAI(body []byte, model string) ([]byte, error)
func XAIResponseToChatCompletions(body []byte, stream bool) ([]byte, error)
func ResponsesToXAI(body []byte, model string) ([]byte, error)
func XAIEventToResponsesSSE(line []byte) ([]byte, error) // may return nil to skip
func XAIResponseToResponses(body []byte) ([]byte, error)
```

- [ ] **Step 1: Fixture tests**

Non-stream chat:
- Input OpenAI chat body with one user message → xAI responses body has `model`, `input` or `messages` shape expected by xAI (match CPA/xAI: Responses API uses `input` array; tools mapped to xAI tool schema)
- Reverse: xAI completed response → OpenAI chat completion with `choices[0].message`

Stream: convert a few canned SSE `response.output_text.delta` style events into chat `data: {"choices":[{"delta":{"content":"..."}}]}` chunks ending with `data: [DONE]`

- [ ] **Step 2: Implement minimal correct mapping; prefer explicit JSON structs over opaque transforms**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: openai chat/responses translators for xAI"
```

---

### Task 11: Translator — Claude Messages ↔ xAI Responses

**Files:**
- Create: `grok-bridge/internal/translate/claude.go`
- Create: `grok-bridge/internal/translate/claude_test.go`

**Interfaces:**
```go
func ClaudeMessagesToXAI(body []byte, model string) ([]byte, error)
func XAIResponseToClaudeMessage(body []byte) ([]byte, error)
func XAIEventToClaudeSSE(eventType string, data []byte) ([][]byte, error) // zero or more SSE frames
```

- [ ] **Step 1: Tests with Claude request containing system + user + tools; expect xAI tools + instructions/input**

Stream path: map to Claude SSE events `message_start`, `content_block_delta`, `message_delta`, `message_stop`.

- [ ] **Step 2: Implement + commit**

```bash
git commit -m "feat: claude messages translator for xAI"
```

---

### Task 12: xAI executor (HTTP + SSE)

**Files:**
- Create: `grok-bridge/internal/executor/xai/client.go`
- Create: `grok-bridge/internal/executor/xai/client_test.go`

**Interfaces:**
```go
type Client struct {
    HTTP *http.Client
}
type Result struct {
    StatusCode int
    Header http.Header
    Body []byte // non-stream
}
func ChatBaseURL(account account.Account) string
// OAuth default → CLIChatProxyBaseURL unless account.BaseURL set non-default
func (c *Client) DoResponses(ctx context.Context, account account.Account, body []byte, stream bool) (*http.Response, error)
```

Headers for CLI chat-proxy path (from CPA):
- `Authorization: Bearer <access_token>`
- When using CLI chat-proxy: `X-XAI-Token-Auth: xai-grok-cli`, `x-grok-client-version: 0.2.93` (keep constant, comment to bump when needed)

- [ ] **Step 1: Tests with httptest.Server asserting path `/responses`, auth header, stream Accept**

- [ ] **Step 2: Implement `DoResponses` POST `{base}/responses` with JSON body; set `Accept: text/event-stream` when stream**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: xAI responses HTTP executor"
```

---

### Task 13: Pipeline (pick account, translate, execute, retry, log)

**Files:**
- Create: `grok-bridge/internal/pipeline/pipeline.go`
- Create: `grok-bridge/internal/pipeline/pipeline_test.go`
- Create: `grok-bridge/internal/logging/store.go`
- Create: `grok-bridge/internal/logging/store_test.go`

**Interfaces:**
```go
type RequestLogStore struct { DB *sql.DB }
func (s *RequestLogStore) Insert(ctx context.Context, rec LogRecord) error
func (s *RequestLogStore) Query(ctx context.Context, f LogFilter) ([]LogRecord, error)
func (s *RequestLogStore) Dashboard(ctx context.Context) (DashboardStats, error)

type Pipeline struct {
    Accounts *account.Picker
    AccountStore *account.Store
    XAI *xai.Client
    OAuth *xaiauth.Client
    Catalog *models.Catalog
    Logs *logging.RequestLogStore
    Retry config.RetryConfig
    LogBodies string
}
type Inbound struct {
    Protocol translate.Format
    Model string
    Body []byte
    Stream bool
    APIKey *access.KeyRecord
    Path string
    ClientIP string
    UserAgent string
}
func (p *Pipeline) Handle(ctx context.Context, in Inbound, w http.ResponseWriter) error
```

Retry logic:
1. Pick account
2. If token near expiry (`expires_at` within 5m), refresh and update store
3. Execute
4. On 401: refresh once, retry same account; on fail mark error and switch account (max switches)
5. On 429/5xx: switch account up to `max_account_switches`
6. Always write log row (bodies per policy)

- [ ] **Step 1: logging store unit tests**

- [ ] **Step 2: pipeline test with fake HTTP upstream + 2 accounts; first returns 500, second 200**

- [ ] **Step 3: Implement + commit**

```bash
git commit -m "feat: request pipeline with retry and logging"
```

---

### Task 14: Public HTTP routes + API key middleware

**Files:**
- Modify: `grok-bridge/internal/api/server.go`
- Create: `grok-bridge/internal/api/middleware.go`
- Create: `grok-bridge/internal/api/public.go`
- Modify: `grok-bridge/internal/api/public_test.go`
- Modify: `grok-bridge/cmd/server/main.go` to wire deps

**Routes:**
- `POST /v1/messages`, `POST /openai/v1/messages` (optional same handler)
- `POST /v1/messages/count_tokens` — return best-effort estimate JSON or `501` with clear message (prefer rough char/4 estimate for v1)
- `POST /v1/chat/completions`, `POST /openai/v1/chat/completions`
- `POST /v1/responses`, `POST /openai/v1/responses`
- `POST /v1/responses/compact` — return `501` JSON `{"error":"compact not implemented"}` for v1 unless easy
- `GET /v1/models`, `GET /openai/v1/models` — Claude shape if `anthropic-version` header present else OpenAI shape
- Middleware: extract Bearer or `x-api-key`; verify; 401 if missing/invalid

- [ ] **Step 1: Integration-style tests with httptest + temp DB + fake upstream**

```go
func TestChatCompletionsRequiresAPIKey(t *testing.T) { /* 401 without key */ }
func TestChatCompletionsOK(t *testing.T) { /* create key, mock xai, expect 200 */ }
```

- [ ] **Step 2: Implement handlers calling pipeline**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: public Claude/OpenAI proxy routes with API key auth"
```

---

### Task 15: Admin auth + Admin REST API

**Files:**
- Create: `grok-bridge/internal/api/admin.go`
- Create: `grok-bridge/internal/api/admin_test.go`
- Modify: `grok-bridge/internal/api/server.go`

**Admin auth:**
- `POST /admin/api/login` body `{"password":"..."}` → set HTTP-only cookie `gb_admin` with random session token stored in memory map (or settings table) with TTL
- Middleware on `/admin/api/*` except login
- Compare password to `config.Admin.Password` using `subtle.ConstantTimeCompare`

**Endpoints to implement (all JSON):**
- `GET /admin/api/dashboard`
- `GET /admin/api/accounts`
- `POST /admin/api/accounts/import` (raw body JSON or multipart file field `file`)
- `GET /admin/api/accounts/{id}/export`
- `PATCH /admin/api/accounts/{id}` body `{"status":"disabled","label":"..."}`
- `DELETE /admin/api/accounts/{id}`
- `POST /admin/api/accounts/{id}/refresh`
- `POST /admin/api/accounts/oauth/start` → device code payload
- `POST /admin/api/accounts/oauth/poll` → complete + upsert
- `GET/POST/DELETE /admin/api/keys`
- `GET /admin/api/logs?from=&to=&account_id=&model=&status=`
- `GET /admin/api/logs/{id}`
- `GET/PUT /admin/api/settings` (subset: log_bodies, retention)

- [ ] **Step 1: Tests for login failure/success, import account, create key, list logs empty**

- [ ] **Step 2: Implement + commit**

```bash
git commit -m "feat: admin API for accounts keys logs dashboard"
```

---

### Task 16: Embedded Admin UI

**Files:**
- Create: `grok-bridge/internal/adminui/embed.go`
- Create: `grok-bridge/internal/adminui/static/index.html`
- Create: `grok-bridge/internal/adminui/static/app.js`
- Create: `grok-bridge/internal/adminui/static/styles.css`
- Modify: `grok-bridge/internal/api/server.go` to serve `/admin/` and `/admin/static/`

**UI pages (single `index.html` SPA):**
1. Login
2. Dashboard cards
3. Accounts table + import file input + OAuth start modal + enable/disable/delete/export
4. API keys table + create modal (show plaintext once)
5. Logs table + filters + detail drawer
6. Settings form

Use `fetch('/admin/api/...')` with credentials. No npm build.

```go
// embed.go
package adminui
import "embed"
//go:embed static/*
var Static embed.FS
```

- [ ] **Step 1: Manual checklist after implement** (automated smoke: GET `/admin/` returns 200 HTML)

```go
func TestAdminIndexEmbedded(t *testing.T) {
	// server with embed; GET /admin/ or /admin/index.html → 200 and contains "Grok Bridge"
}
```

- [ ] **Step 2: Implement minimal clean UI**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat: embedded admin web UI"
```

---

### Task 17: Wire main, Docker, README end-to-end docs

**Files:**
- Modify: `grok-bridge/cmd/server/main.go` (full wiring)
- Modify: `grok-bridge/cmd/login/main.go`
- Create: `grok-bridge/Dockerfile`
- Create: `grok-bridge/docker-compose.yml`
- Modify: `grok-bridge/README.md`

**main wiring order:**
1. flag `-config` default `config.yaml`
2. Load config + ApplyEnv
3. Open DB + Migrate
4. Construct stores, picker, oauth client, executor, catalog, pipeline
5. `api.NewServer(deps)`
6. Listen

Dockerfile:
```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY . .
RUN go build -o /out/grok-bridge ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/grok-bridge /usr/local/bin/grok-bridge
EXPOSE 8080
ENTRYPOINT ["grok-bridge"]
CMD ["-config", "/config/config.yaml"]
```

README sections:
- What it is
- Quick start (config, login/import, create API key, point Claude Code / Codex)
- Claude Code env example: `ANTHROPIC_BASE_URL=http://127.0.0.1:8080` + `ANTHROPIC_API_KEY=<gb_...>`
- Codex/OpenAI base URL example
- Admin at `/admin`
- Security notes (TLS reverse proxy, bypass risks)

- [ ] **Step 1: `go test ./...` and `go build` both cmds**

```bash
cd grok-bridge && go test ./... && go build -o /tmp/grok-bridge ./cmd/server && go build -o /tmp/grok-bridge-login ./cmd/login
```

- [ ] **Step 2: Commit**

```bash
git commit -m "docs: docker and README for grok-bridge"
```

---

### Task 18: End-to-end verification checklist (manual + automated smoke)

**Files:**
- Create: `grok-bridge/scripts/smoke.sh` (optional)
- Or document in README

- [ ] **Step 1: Automated smoke without real xAI**

Use existing unit/integration tests; ensure:
```bash
cd grok-bridge && go test ./... 
```
all pass.

- [ ] **Step 2: Manual checklist (operator)**

1. Start server with admin password set  
2. Open `/admin`, login  
3. Import sample xAI JSON (or OAuth login)  
4. Create API key  
5. `curl -H "Authorization: Bearer gb_..." http://127.0.0.1:8080/v1/models`  
6. With real account: Claude Code / Codex one chat  
7. Confirm log row + dashboard count  

- [ ] **Step 3: Final commit if any fixes**

```bash
git commit -m "test: smoke coverage and verification fixes"
```

---

## Self-Review (spec coverage)

| Spec section | Tasks |
|--------------|-------|
| Claude + Codex inbound | 10, 11, 14 |
| xAI OAuth only | 6, 15 (admin oauth), login cmd |
| Streaming, tools, thinking | 9–13 |
| Multi-account RR + retry | 7, 13 |
| Client API keys | 4, 14 |
| JSON import/export | 5, 15, 16 |
| Web admin dashboard/accounts/keys/logs/settings | 15, 16 |
| SQLite + embed UI | 3, 16 |
| Config + Docker | 2, 17 |
| Security (hash keys, admin separate, redact) | 4, 5 export/list redaction in admin list DTOs, 15 |
| Out of scope respected | no image/video/WS tasks |

**Placeholder scan:** no TBD/TODO steps; compact/count_tokens explicitly stubbed with behavior.  
**Type consistency:** `account.Account`, `access.KeyRecord`, `logging.LogRecord`, `pipeline.Inbound` names reused across tasks.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-07-13-grok-bridge.md`.

**Two execution options:**

1. **Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, faster iteration  
2. **Inline Execution** — same session with executing-plans, batched with checkpoints  

Which approach?
