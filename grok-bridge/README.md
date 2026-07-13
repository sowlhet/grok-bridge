# grok-bridge

Independent Go proxy that lets **Claude Code** and **Codex** use **xAI Grok** (Build OAuth) as the upstream model backend.

It speaks Anthropic Messages and OpenAI Chat/Responses on the public side, translates to xAI, and manages multi-account OAuth, client API keys, request logs, and an embedded admin UI.

## Features

- Public APIs: Claude Messages, OpenAI Chat Completions, OpenAI/xAI Responses, models list
- Client API keys (`gb_…`), hashed at rest
- Multi-account round-robin with token auto-refresh and failover
- CPA-style account JSON import + OAuth device-flow login CLI
- Request logging and dashboard aggregates
- Embedded admin SPA at `/admin`

## Quick start

### 1. Config

```bash
cp config.example.yaml config.yaml
# set a strong admin.password (or export GROK_BRIDGE_ADMIN_PASSWORD)
# Server refuses empty password and the insecure default "change-me".
```

Useful env overrides:

| Variable | Purpose |
|----------|---------|
| `GROK_BRIDGE_LISTEN` | Public listen address (default `0.0.0.0:8080`) |
| `GROK_BRIDGE_ADMIN_LISTEN` | Optional admin-only listen address (split ports) |
| `GROK_BRIDGE_ADMIN_PASSWORD` | Admin password (required; not `change-me`) |
| `GROK_BRIDGE_SQLITE_PATH` | SQLite path |

### 2. Run the server

```bash
go run ./cmd/server -config config.yaml
# or
go build -o grok-bridge ./cmd/server && ./grok-bridge -config config.yaml

curl -s localhost:8080/healthz   # ok
```

### 3. Add an xAI account

**OAuth device flow (CLI):**

```bash
go run ./cmd/login -config config.yaml
```

**Or** open the admin UI → Accounts → import CPA-style JSON / start OAuth.

### 4. Create a client API key

Open `http://127.0.0.1:8080/admin`, sign in with the admin password, create an API key.  
The plaintext `gb_…` key is shown **once** at creation — copy it.

### 5. Point clients at the bridge

#### Claude Code

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8080
export ANTHROPIC_API_KEY=gb_your_key_here
```

Claude Code will call `/v1/messages` on the bridge; model aliases in `config.yaml` map Claude model IDs to Grok (e.g. `claude-sonnet-4-20250514` → `grok-4.5`).

#### Codex / OpenAI-compatible clients

```bash
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
# or http://127.0.0.1:8080/openai/v1
export OPENAI_API_KEY=gb_your_key_here
```

Chat Completions: `POST /v1/chat/completions`  
Responses: `POST /v1/responses`

## Admin

- UI: `http://127.0.0.1:8080/admin` (or the `admin_listen` host when split)
- REST under `/admin/api/*` (session cookie after login)
- Manage accounts, API keys, request logs, and settings
- Optional `server.admin_listen` / `GROK_BRIDGE_ADMIN_LISTEN`: public API stays on `listen`; admin UI/API only on `admin_listen`

## Docker

```bash
cp config.example.yaml config.yaml
export GROK_BRIDGE_ADMIN_PASSWORD='your-strong-password'   # required
mkdir -p data
docker compose up --build -d
```

Compose **requires** `GROK_BRIDGE_ADMIN_PASSWORD` in the environment (compose fails to start without it).  
Mounts `./config.yaml` → `/config/config.yaml` and `./data` for SQLite.  
The image entrypoint is the server binary with `-config /config/config.yaml`.

Login CLI is not in the image; run `go run ./cmd/login` on the host against the same SQLite volume, or import accounts via the admin UI.

## Security notes

- **Do not expose the public port raw on the internet.** Put a TLS reverse proxy (Caddy, nginx, Traefik) in front; prefer HTTPS only.
- Admin password and OAuth tokens protect real Grok capacity — use a strong `GROK_BRIDGE_ADMIN_PASSWORD`, keep `config.yaml` and the SQLite file private (DB file is created `0600`, data dir `0700`).
- Server **refuses to start** if admin password is empty or `change-me`.
- Client API keys are hashed at rest; treat plaintext `gb_…` keys like passwords.
- Prefer `admin_listen` bound to localhost (or a private interface) so admin is not reachable on the public port.
- Request body logging (`proxy.log_bodies`) can capture prompts — keep `errors_only` or `off` in shared environments. Logged bodies are scrubbed for Bearer tokens, API keys, and OAuth token JSON fields.
- Old request logs are purged hourly according to `proxy.log_retention_days`.
- This proxy is a **bypass path** relative to official Anthropic/OpenAI endpoints: anyone with a valid bridge key and a live xAI account can spend Grok quota. Rotate keys, disable accounts, and restrict network access accordingly.
- Health endpoint (`/healthz`) is unauthenticated and must not leak account/key material (it returns `ok` only).

## Development

```bash
go test ./...
go build -o /tmp/grok-bridge ./cmd/server
go build -o /tmp/grok-bridge-login ./cmd/login
```

### Automated smoke (no real xAI)

`scripts/smoke.sh` boots a temp server, checks healthz, admin login, key create, `/v1/models` 401/200, and dashboard:

```bash
./scripts/smoke.sh
# optional: SMOKE_PORT=18081 SMOKE_KEEP_TMP=1 ./scripts/smoke.sh
```

### Manual verification checklist

Use after deploy or when wiring a real xAI account:

1. Start server with admin password set (`config.yaml` or `GROK_BRIDGE_ADMIN_PASSWORD`)
2. Open `http://127.0.0.1:8080/admin`, log in
3. Import sample xAI JSON (Accounts → Import) or run `go run ./cmd/login -config config.yaml`
4. Create an API key; copy the one-time `gb_…` plaintext
5. `curl -H "Authorization: Bearer gb_..." http://127.0.0.1:8080/v1/models` → 200 list
6. With a live account: one Claude Code chat (`ANTHROPIC_BASE_URL` / `ANTHROPIC_API_KEY`) and/or one Codex chat (`OPENAI_BASE_URL` / `OPENAI_API_KEY`)
7. Confirm a request log row under Logs and non-zero counts on the dashboard

See `config.example.yaml` for the full configuration surface.
