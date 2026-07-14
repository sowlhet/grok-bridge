#!/usr/bin/env bash
# Build portable release archives for macOS and Windows x64.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)}"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OUT="${OUT_DIR:-$ROOT/dist}"
mkdir -p "$OUT"

LDFLAGS="-s -w -X main.Version=${VERSION} -X main.Commit=$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo none) -X main.BuildDate=${DATE}"

build_one() {
  local goos="$1" goarch="$2" name="$3"
  local dir="$OUT/${name}"
  local bin="grok-bridge"
  if [[ "$goos" == "windows" ]]; then
    bin="grok-bridge.exe"
  fi

  echo "==> building ${name} (GOOS=${goos} GOARCH=${goarch})"
  rm -rf "$dir"
  mkdir -p "$dir"

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$bin" ./cmd/server

  local login_bin="grok-bridge-login"
  if [[ "$goos" == "windows" ]]; then
    login_bin="grok-bridge-login.exe"
  fi
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$login_bin" ./cmd/login

  cp "$ROOT/config.example.yaml" "$dir/config.example.yaml"
  cp "$ROOT/README.md" "$dir/README.md"
  if [[ -f "$ROOT/packaging/README-RELEASE.md" ]]; then
    cp "$ROOT/packaging/README-RELEASE.md" "$dir/README-RELEASE.md"
  fi

  if [[ "$goos" == "windows" ]]; then
    cat > "$dir/start.bat" <<'BAT'
@echo off
setlocal
cd /d %~dp0
if not exist config.yaml copy config.example.yaml config.yaml >nul
if "%GROK_BRIDGE_ADMIN_PASSWORD%"=="" set GROK_BRIDGE_ADMIN_PASSWORD=change-me-now
if not exist data mkdir data
echo Starting Grok Bridge on http://127.0.0.1:8080
echo Admin UI: http://127.0.0.1:8080/admin
echo Set a strong GROK_BRIDGE_ADMIN_PASSWORD before real use.
grok-bridge.exe -config config.yaml
pause
BAT
    cat > "$dir/install-service.ps1" <<'PS1'
# Optional: install as a Windows service using NSSM if available.
# Usage (Admin PowerShell): .\install-service.ps1
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Bin = Join-Path $Root "grok-bridge.exe"
$Config = Join-Path $Root "config.yaml"
if (-not (Test-Path $Config)) { Copy-Item (Join-Path $Root "config.example.yaml") $Config }
if (-not $env:GROK_BRIDGE_ADMIN_PASSWORD) {
  Write-Host "Set GROK_BRIDGE_ADMIN_PASSWORD first." -ForegroundColor Yellow
}
$nssm = Get-Command nssm -ErrorAction SilentlyContinue
if (-not $nssm) {
  Write-Host "NSSM not found. Run start.bat for portable mode, or install NSSM and re-run." -ForegroundColor Yellow
  exit 1
}
& nssm install GrokBridge $Bin "-config" $Config
& nssm set GrokBridge AppDirectory $Root
& nssm set GrokBridge AppEnvironmentExtra "GROK_BRIDGE_ADMIN_PASSWORD=$env:GROK_BRIDGE_ADMIN_PASSWORD"
& nssm start GrokBridge
Write-Host "Installed and started service GrokBridge"
PS1
  else
    cat > "$dir/start.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
if [[ ! -f config.yaml ]]; then
  cp config.example.yaml config.yaml
  echo "Created config.yaml from example. Edit admin password before production use."
fi
export GROK_BRIDGE_ADMIN_PASSWORD="${GROK_BRIDGE_ADMIN_PASSWORD:-change-me-now}"
mkdir -p data
echo "Starting Grok Bridge on http://127.0.0.1:8080"
echo "Admin UI: http://127.0.0.1:8080/admin"
exec ./grok-bridge -config config.yaml
SH
    chmod +x "$dir/start.sh" "$dir/grok-bridge" "$dir/grok-bridge-login" 2>/dev/null || true
  fi

  # Write version file
  cat > "$dir/VERSION.txt" <<VER
version=${VERSION}
os=${goos}
arch=${goarch}
built_at=${DATE}
VER

  # Archive
  (
    cd "$OUT"
    if [[ "$goos" == "windows" ]]; then
      zip -qr "${name}.zip" "${name}"
      echo "    -> ${OUT}/${name}.zip"
    else
      tar -czf "${name}.tar.gz" "${name}"
      echo "    -> ${OUT}/${name}.tar.gz"
    fi
  )
}

# Targets requested: macOS (arm64+amd64) and Windows x64
build_one darwin arm64 "grok-bridge_${VERSION}_darwin_arm64"
build_one darwin amd64 "grok-bridge_${VERSION}_darwin_amd64"
build_one windows amd64 "grok-bridge_${VERSION}_windows_amd64"

echo
echo "Done. Artifacts in: $OUT"
ls -lh "$OUT"/*.{tar.gz,zip} 2>/dev/null || ls -lh "$OUT"
