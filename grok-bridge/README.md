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
# set admin.password (or use GROK_BRIDGE_ADMIN_PASSWORD)
```

Useful env overrides:

| Variable | Purpose |
|----------|---------|
| `GROK_BRIDGE_LISTEN` | Listen address (default `0.0.0.0:8080`) |
| `GROK_BRIDGE_ADMIN_PASSWORD` | Admin password |
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

- UI: `http://127.0.0.1:8080/admin`
- REST under `/admin/api/*` (session cookie after login)
- Manage accounts, API keys, request logs, and settings

## Docker

```bash
cp config.example.yaml config.yaml
# edit config / set GROK_BRIDGE_ADMIN_PASSWORD
mkdir -p data
docker compose up --build -d
```

Compose mounts `./config.yaml` → `/config/config.yaml` and `./data` for SQLite.  
The image entrypoint is the server binary with `-config /config/config.yaml`.

Login CLI is not in the image; run `go run ./cmd/login` on the host against the same SQLite volume, or import accounts via the admin UI.

## Security notes

- **Do not expose the public port raw on the internet.** Put a TLS reverse proxy (Caddy, nginx, Traefik) in front; prefer HTTPS only.
- Admin password and OAuth tokens protect real Grok capacity — use a strong `GROK_BRIDGE_ADMIN_PASSWORD`, keep `config.yaml` and the SQLite file private (`data/` permissions).
- Client API keys are hashed at rest; treat plaintext `gb_…` keys like passwords.
- Optional `admin_listen` / binding admin to localhost reduces remote admin surface when you terminate TLS elsewhere.
- Request body logging (`proxy.log_bodies`) can capture prompts — keep `errors_only` or `off` in shared environments.
- This proxy is a **bypass path** relative to official Anthropic/OpenAI endpoints: anyone with a valid bridge key and a live xAI account can spend Grok quota. Rotate keys, disable accounts, and restrict network access accordingly.
- Health endpoint (`/healthz`) is unauthenticated and must not leak account/key material (it returns `ok` only).

## Development

```bash
go test ./...
go build -o /tmp/grok-bridge ./cmd/server
go build -o /tmp/grok-bridge-login ./cmd/login
```

See `config.example.yaml` for the full configuration surface.
