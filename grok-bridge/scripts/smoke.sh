#!/usr/bin/env bash
# Local smoke test without a real xAI account.
# Covers: healthz, admin login, create key, /v1/models 401/200, dashboard.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PORT="${SMOKE_PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"
ADMIN_PASSWORD="${SMOKE_ADMIN_PASSWORD:-smoke-admin-pass}"
TMPDIR="${TMPDIR:-/tmp}"
WORKDIR="$(mktemp -d "${TMPDIR%/}/grok-bridge-smoke.XXXXXX")"
CONFIG="${WORKDIR}/config.yaml"
DB="${WORKDIR}/smoke.db"
COOKIE_JAR="${WORKDIR}/cookies.txt"
SERVER_LOG="${WORKDIR}/server.log"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  if [[ "${SMOKE_KEEP_TMP:-}" != "1" ]]; then
    rm -rf "${WORKDIR}"
  else
    echo "kept workdir: ${WORKDIR}" >&2
  fi
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  if [[ -f "${SERVER_LOG}" ]]; then
    echo "--- server log ---" >&2
    tail -n 50 "${SERVER_LOG}" >&2 || true
  fi
  exit 1
}

pass() {
  echo "OK: $*"
}

echo "smoke workdir: ${WORKDIR}"

cat >"${CONFIG}" <<EOF
server:
  listen: "127.0.0.1:${PORT}"
admin:
  password: "${ADMIN_PASSWORD}"
  session_ttl: 1h
data:
  sqlite_path: "${DB}"
proxy:
  retry:
    max_account_switches: 1
    max_transient_retries: 1
  log_bodies: "errors_only"
  log_retention_days: 7
  unknown_model: "passthrough"
models:
  - id: grok-4.5
  - id: grok-3-mini
aliases:
  "claude-sonnet-4-20250514": "grok-4.5"
  "gpt-5": "grok-4.5"
xai: {}
EOF

echo "building server..."
go build -o "${WORKDIR}/grok-bridge" ./cmd/server

echo "starting server on ${BASE}..."
"${WORKDIR}/grok-bridge" -config "${CONFIG}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

# Wait for healthz
for i in $(seq 1 50); do
  if curl -sf "${BASE}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    fail "server exited early"
  fi
  sleep 0.1
done

# --- 1. healthz ---
HZ="$(curl -sf "${BASE}/healthz" || true)"
[[ "${HZ}" == "ok" ]] || fail "healthz body=${HZ}"
pass "GET /healthz → ok"

# --- 2. models without key → 401 ---
CODE="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/v1/models")"
[[ "${CODE}" == "401" ]] || fail "/v1/models without key status=${CODE} want 401"
pass "GET /v1/models without key → 401"

# --- 3. admin login wrong password → 401 ---
CODE="$(curl -s -o /dev/null -w '%{http_code}' \
  -X POST "${BASE}/admin/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"password":"wrong"}')"
[[ "${CODE}" == "401" ]] || fail "bad login status=${CODE} want 401"
pass "POST /admin/api/login wrong password → 401"

# --- 4. admin login ok + cookie ---
CODE="$(curl -s -o "${WORKDIR}/login.json" -w '%{http_code}' \
  -c "${COOKIE_JAR}" \
  -X POST "${BASE}/admin/api/login" \
  -H 'Content-Type: application/json' \
  -d "{\"password\":\"${ADMIN_PASSWORD}\"}")"
[[ "${CODE}" == "200" ]] || fail "login status=${CODE} body=$(cat "${WORKDIR}/login.json")"
grep -q 'gb_admin' "${COOKIE_JAR}" || fail "missing gb_admin cookie"
pass "POST /admin/api/login → 200 + gb_admin cookie"

# --- 5. dashboard (session) ---
CODE="$(curl -s -o "${WORKDIR}/dash.json" -w '%{http_code}' \
  -b "${COOKIE_JAR}" \
  "${BASE}/admin/api/dashboard")"
[[ "${CODE}" == "200" ]] || fail "dashboard status=${CODE} body=$(cat "${WORKDIR}/dash.json")"
pass "GET /admin/api/dashboard → 200"

# --- 6. create API key ---
CODE="$(curl -s -o "${WORKDIR}/key.json" -w '%{http_code}' \
  -b "${COOKIE_JAR}" \
  -X POST "${BASE}/admin/api/keys" \
  -H 'Content-Type: application/json' \
  -d '{"label":"smoke"}')"
[[ "${CODE}" == "201" ]] || fail "create key status=${CODE} body=$(cat "${WORKDIR}/key.json")"
KEY="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["key"])' "${WORKDIR}/key.json")"
[[ "${KEY}" == gb_* ]] || fail "unexpected key format: ${KEY}"
pass "POST /admin/api/keys → 201 key=${KEY:0:8}…"

# --- 7. models with key → 200 ---
CODE="$(curl -s -o "${WORKDIR}/models.json" -w '%{http_code}' \
  -H "Authorization: Bearer ${KEY}" \
  "${BASE}/v1/models")"
[[ "${CODE}" == "200" ]] || fail "/v1/models with key status=${CODE} body=$(cat "${WORKDIR}/models.json")"
python3 -c '
import json,sys
d=json.load(open(sys.argv[1]))
# OpenAI shape: {"object":"list","data":[...]} or data array
data=d.get("data") if isinstance(d,dict) else d
if not data:
    raise SystemExit("empty models list: "+json.dumps(d)[:200])
print("models:", len(data))
' "${WORKDIR}/models.json" || fail "models JSON shape"
pass "GET /v1/models with key → 200 + non-empty list"

# --- 8. admin UI index ---
CODE="$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/admin")"
[[ "${CODE}" == "200" ]] || fail "GET /admin status=${CODE}"
pass "GET /admin → 200"

# --- 9. list keys (redacted) ---
CODE="$(curl -s -o "${WORKDIR}/keys.json" -w '%{http_code}' \
  -b "${COOKIE_JAR}" \
  "${BASE}/admin/api/keys")"
[[ "${CODE}" == "200" ]] || fail "list keys status=${CODE}"
pass "GET /admin/api/keys → 200"

echo
echo "smoke passed (no real xAI upstream exercised)"
