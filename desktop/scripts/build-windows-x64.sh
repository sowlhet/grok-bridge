#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

echo "==> Build Windows Go sidecar"
mkdir -p desktop/src-tauri/binaries
(
  cd grok-bridge
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w"     -o ../desktop/src-tauri/binaries/grok-bridge-x86_64-pc-windows-msvc.exe ./cmd/server
)

echo "==> Build Tauri Windows desktop (requires cargo-xwin + WebView2 headers)"
cd desktop
npm install
# Prefer CI on Windows. Local cross-build is best-effort.
if command -v cargo-xwin >/dev/null 2>&1; then
  npm run tauri build -- --runner cargo-xwin --target x86_64-pc-windows-msvc || {
    echo "Local cross-build failed. Use GitHub Actions workflow desktop-windows.yml instead." >&2
    exit 1
  }
else
  echo "cargo-xwin not found; use GitHub Actions workflow." >&2
  exit 1
fi
