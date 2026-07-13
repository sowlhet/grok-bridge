# Grok Bridge Design

**Date:** 2026-07-13  
**Status:** Draft for review  
**Working title:** `grok-bridge` (final repo/binary name can change)

## 1. Goal

Build a **focused, independent Go proxy** that lets **Claude Code** and **Codex** use **xAI Grok Build (OAuth)** as the upstream model backend, with:

- Client access control (API keys)
- Multi-account Grok OAuth pool + round-robin
- Account JSON import/export (CLIProxyAPI-compatible)
- Embedded Web admin: accounts, API keys, request logs, dashboard

This is **not** a full multi-provider clone of CLIProxyAPI. It intentionally supports only:

- Inbound: Claude Messages + OpenAI/Codex-compatible APIs  
- Outbound: xAI Grok (OAuth / Grok Build)

Reference implementation for Grok behavior: `CLIProxyAPI` (`internal/auth/xai`, `internal/runtime/executor/xai_executor.go`, thinking/xAI paths). Logic is **reimplemented in a smaller codebase**, not by forking the whole monorepo.

## 2. Confirmed requirements

| Area | Decision |
|------|----------|
| Shape | Standalone slim proxy project |
| Clients | Claude Code + Codex (primary) |
| Upstream auth | xAI OAuth only (Grok Build subscription) |
| Capability level | Practical-complete: streaming, tools, thinking, multi-account, model list, basic retry |
| Implementation | Independent Go rewrite guided by CLIProxyAPI |
| Access | Remote-capable; client API keys |
| Admin | Web UI: accounts, logs, keys, dashboard |
| Storage / UI | SQLite + embedded Web (single binary) |
| Account ops | Import/export JSON, enable/disable, OAuth login, token refresh |

### Out of scope (v1)

- Other upstreams (Gemini, Antigravity, Claude/Codex OAuth as upstream, etc.)
- Image/video generation endpoints
- WebSocket upstream transport
- Billing / invoicing
- Multi-tenant RBAC / SSO
- Multi-instance shared cluster (Postgres) — can come later
- Full feature parity with every CLIProxyAPI edge case

## 3. Approaches considered

### A. Grok Bridge + embedded admin (chosen)

Single Go binary: public proxy API + admin API + embedded SPA/static UI + SQLite.

- Pros: matches product need, simple deploy, focused scope  
- Cons: more work than a pure proxy stub  

### B. Thin wrapper around CLIProxyAPI

Configure/run CPA and add a small shell around it.

- Pros: fastest protocol compatibility  
- Cons: not an independent slim project; heavy dependency surface  

### C. Mini framework clone of CPA abstractions

Copy full provider/translator/registry architecture first.

- Pros: easy later expansion  
- Cons: over-abstracted for a single-upstream product  

**Choice:** A.

## 4. Architecture

```
                 ┌──────────────────────────────────────┐
 Claude Code ──► │  Public API (:8080)                  │
 Codex       ──► │  Authorization: Bearer <api-key>     │
                 │  /v1/messages  /v1/responses         │
                 │  /v1/chat/completions  /v1/models    │
                 └──────────────┬───────────────────────┘
                                │
                                ▼
                 ┌──────────────────────────────────────┐
                 │  Pipeline                            │
                 │  auth key → translate → pick account │
                 │  → xAI execute → translate back      │
                 │  → write request log                 │
                 └──────────────┬───────────────────────┘
                                │
                 ┌──────────────▼───────────────────────┐
                 │  SQLite                              │
                 │  accounts | api_keys | request_logs  │
                 │  daily_stats | settings              │
                 └──────────────────────────────────────┘
                                ▲
                 ┌──────────────┴───────────────────────┐
 Browser     ──► │  Admin UI (embedded at /admin)       │
                 │  /admin/api/* (admin session/token)  │
                 │  accounts · keys · logs · dashboard  │
                 └──────────────────────────────────────┘
```

### Process model

- One binary: `grok-bridge`
- Optional helper: `grok-bridge login` (CLI OAuth into same DB)
- Default single listen address; optional separate `admin_listen` for binding admin to localhost only

### Module layout

```
grok-bridge/
  cmd/server/           # HTTP server + DB migrate
  cmd/login/            # optional CLI OAuth login
  internal/
    config/             # yaml + env overrides
    db/                 # sqlite open, migrations
    access/             # client API key verify (hashed)
    auth/xai/           # device flow, refresh, discovery
    account/            # CRUD, import/export, picker
    executor/xai/       # upstream HTTP + SSE
    translate/
      claude/           # Anthropic Messages ↔ xAI Responses
      openai/           # chat.completions / responses ↔ xAI
    thinking/           # reasoning field mapping
    logging/            # request log writer + query
    adminapi/           # management REST
    adminui/            # go:embed static assets
  config.example.yaml
  Dockerfile
  docker-compose.yml
  README.md
```

## 5. Data model (SQLite)

### 5.1 `accounts`

| Column | Notes |
|--------|--------|
| id | UUID |
| label | display name |
| email, subject | identity / dedupe keys |
| access_token, refresh_token, id_token | secrets |
| token_type, expires_at, last_refresh_at | refresh |
| base_url, token_endpoint | upstream endpoints |
| status | `active` / `disabled` / `expired` / `error` |
| error_message | last failure |
| weight | optional; v1 may treat as equal |
| created_at, updated_at | |

List APIs redact tokens (show suffix only). Full tokens only on authenticated export.

### 5.2 `api_keys`

| Column | Notes |
|--------|--------|
| id | UUID |
| name / label | remark |
| key_prefix | e.g. first 8 chars for display |
| key_hash | SHA-256 (or stronger KDF if desired) |
| enabled | bool |
| last_used_at | |
| created_at | |

Plaintext key shown **once** at creation.

### 5.3 `request_logs`

| Column | Notes |
|--------|--------|
| id, request_id | |
| created_at | |
| api_key_id, api_key_label | |
| account_id, account_label | |
| protocol | `claude` / `openai` / `responses` |
| model_requested, model_upstream | |
| stream | bool |
| status_code | |
| error_code, error_message | short |
| latency_ms | |
| input_tokens, output_tokens | if available |
| client_ip, user_agent, path | optional |
| request_body, response_body | optional; controlled by setting |

### 5.4 `daily_stats` (optional acceleration)

Per-day aggregates: requests, errors, tokens, by account/model. Can be derived from logs first; materialize if query becomes slow.

### 5.5 settings

Key/value or single JSON row for runtime toggles (log body mode, retention, aliases overrides).

## 6. Account import / OAuth

### 6.1 CLIProxyAPI-compatible JSON

Import accepts one object, an array, or a zip of `*.json` files. Compatible with CPA `xai-*.json`:

```json
{
  "type": "xai",
  "auth_kind": "oauth",
  "access_token": "...",
  "refresh_token": "...",
  "id_token": "...",
  "token_type": "Bearer",
  "expires_in": 3600,
  "expired": "2026-07-13T12:00:00Z",
  "last_refresh": "2026-07-13T11:00:00Z",
  "email": "user@example.com",
  "sub": "...",
  "base_url": "",
  "token_endpoint": "https://..."
}
```

Rules:

- Prefer `type == "xai"`; allow missing type if OAuth fields present
- Upsert key: `email` else `sub`
- Existing row → update tokens/metadata; new → insert
- Optional import flag: start as enabled or disabled
- Export uses the same schema for backup / CPA interop

### 6.2 Interactive OAuth

1. Web: start device flow → show verification URL + user code → poll → save  
2. CLI: `grok-bridge login` → same device flow → write DB  

Refresh: proactive before expiry; on 401 refresh once and retry; permanent failure marks `error` and skips in picker.

### 6.3 Account picker

```
active accounts → round-robin
  on 401 → refresh + retry once → else mark error, switch account
  on 429/5xx → limited switch/retry
  no healthy account → 503 with clear message
```

Disabled accounts never selected.

## 7. Public proxy API

All proxy routes (except health) require client API key:

- `Authorization: Bearer <api-key>`
- Claude clients may also send `x-api-key` (mapped to the same key)

### Routes

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/v1/messages` | Claude Code |
| POST | `/v1/messages/count_tokens` | token estimate (best-effort) |
| POST | `/v1/responses` | Codex / Responses |
| POST | `/v1/responses/compact` | compact if feasible; else explicit error/degrade |
| POST | `/v1/chat/completions` | OpenAI-compatible chat |
| GET | `/v1/models` | model list (shape depends on client) |
| GET | `/healthz` | liveness (no secrets) |

Compatibility prefixes:

- `/openai/v1/*` mirrors `/v1/*`

### Translation pipeline

1. Detect inbound protocol  
2. Normalize + apply model alias  
3. Translate to xAI Responses request  
4. Apply thinking/reasoning mapping  
5. Execute via selected account (stream or non-stream)  
6. Translate upstream SSE/JSON back to inbound protocol  
7. Persist request log (+ optional stats)

Upstream base URL resolution should follow CPA’s Grok CLI chat-proxy vs default API behavior where needed for Build/OAuth users.

### Models & aliases

- Built-in Grok model catalog (e.g. `grok-4.5`, `grok-4.3`, `grok-3-mini`, …) configurable  
- `aliases` map client model IDs → upstream Grok IDs  
- Unknown models: config `strict` (reject) vs `passthrough`

## 8. Admin Web + API

### UI pages (`/admin`)

1. **Dashboard** — today / 7d volume, error rate, top accounts/models, active account count  
2. **Accounts** — list, enable/disable, delete, refresh, OAuth add, **import/export JSON**, status/expiry  
3. **API Keys** — create (show once), revoke, labels, last used  
4. **Logs** — filter by time, account, model, status, api key, stream; detail view  
5. **Settings** — admin password change, log body mode, retention, retries, aliases  

Auth for admin: password login → session cookie or admin bearer token. **Separate** from client API keys.

### Admin REST (illustrative)

```
POST   /admin/api/login
GET    /admin/api/dashboard
GET    /admin/api/accounts
POST   /admin/api/accounts/import
GET    /admin/api/accounts/:id/export
POST   /admin/api/accounts/oauth/start
POST   /admin/api/accounts/oauth/poll
PATCH  /admin/api/accounts/:id
DELETE /admin/api/accounts/:id
POST   /admin/api/accounts/:id/refresh
GET    /admin/api/keys
POST   /admin/api/keys
DELETE /admin/api/keys/:id
GET    /admin/api/logs
GET    /admin/api/logs/:id
GET    /admin/api/settings
PUT    /admin/api/settings
```

Frontend: lightweight modern admin SPA or server-driven UI, **embedded with `go:embed`**. No separate frontend deploy required.

## 9. Logging policy

Default: **do not store full bodies**.

`log_bodies`: `off` | `errors_only` | `sample` | `all`  
Default recommendation: `errors_only` or `off` for privacy.

Always scrub `Authorization` / tokens if bodies are stored.  
`log_retention_days` default `30` with periodic purge.

## 10. Configuration

```yaml
server:
  listen: "0.0.0.0:8080"
  # admin_listen: "127.0.0.1:8081"

admin:
  password: "change-me"   # prefer env GROK_BRIDGE_ADMIN_PASSWORD
  session_ttl: 24h

data:
  sqlite_path: "./data/grok-bridge.db"

proxy:
  retry:
    max_account_switches: 2
    max_transient_retries: 2
  log_bodies: "errors_only"
  log_retention_days: 30
  unknown_model: "passthrough"  # or strict

models:
  - id: grok-4.5
  - id: grok-4.3
  - id: grok-3-mini

aliases:
  "claude-sonnet-4-20250514": "grok-4.5"
  "gpt-5": "grok-4.5"

xai:
  # base_url usually empty; resolve like CPA for Build/OAuth
```

Sensitive values overridable via environment variables.

## 11. Security

| Topic | Policy |
|-------|--------|
| Client API keys | Hashed at rest; plaintext only at creation |
| Admin | Separate password/session; all `/admin/api/*` protected |
| OAuth tokens | In SQLite; DB file permissions 0600; list redaction |
| Remote use | Document reverse proxy TLS; optional admin bind to localhost |
| CORS | Admin same-origin; public API CORS off or allowlist |
| Import | Admin-only; size limits; schema validation |
| Health | No account/key leakage |
| Admin mutations | Structured logs (import/delete/create key) |

Not in v1: full audit warehouse, WAF, per-key rate limit UI (simple global limits optional later).

## 12. Deployment

- Local: `./grok-bridge --config config.yaml`
- Docker: image + volume for `data/` and config
- `docker-compose.yml` for one-command run
- Single port by default; optional split admin port

## 13. Success criteria (MVP)

1. Import CPA-style xAI account JSON in Web UI; account appears and can be toggled  
2. Create client API key in admin  
3. Claude Code → bridge → Grok works (stream + tools + thinking)  
4. Codex → bridge → Grok works (Responses + stream)  
5. Request logs queryable with filters  
6. Dashboard shows basic volume/error aggregates  
7. Token auto-refresh; failed accounts skipped; multi-account round-robin  
8. Single binary (or binary + static embed) deployable with Docker

## 14. Implementation phases (preview; detailed plan later)

1. Skeleton: config, sqlite migrations, server boot, health  
2. Account store + JSON import/export + OAuth login/refresh  
3. xAI executor (non-stream then stream)  
4. Translators: OpenAI chat/responses, then Claude messages  
5. Client API key middleware + model list/aliases  
6. Request logging + dashboard aggregates  
7. Embedded admin UI wired to admin API  
8. Docker, README, end-to-end manual test checklist  

## 15. Open points (defaults chosen)

| Point | Default |
|-------|---------|
| Project/binary name | `grok-bridge` |
| Dual prefix `/openai/v1` | Yes |
| Log bodies default | `errors_only` |
| Admin port | Same port `/admin`; optional `admin_listen` |
| UI stack | Lightweight embedded SPA (exact lib chosen at implementation) |
| count_tokens / compact | Best-effort; may stub with clear behavior if upstream lacks parity |

## 16. Non-goals reminder

Keep the product a **Grok access bridge for Claude/Codex with operable admin**, not a universal multi-provider AI gateway.
