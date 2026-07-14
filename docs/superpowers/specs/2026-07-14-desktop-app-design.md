# Grok Bridge Desktop App Design

**Date:** 2026-07-14  
**Status:** Approved for implementation  
**Model:** Codex-Manager-style pure desktop app (not browser-first)

## 1. Goal

Ship a real desktop application for **macOS** and **Windows x64** that:

- Opens as a native app window (not “start a server then open browser”)
- Stays resident in the system tray / menu bar
- Automatically runs the existing `grok-bridge` Go backend
- Reuses the current Chinese admin UI inside the desktop window
- Continues to expose local API endpoints for Claude Code / Codex

This matches the Codex-Manager model: **desktop shell + local service process**.

## 2. Confirmed product decisions

| Item | Decision |
|------|----------|
| Product shape | Pure desktop app with tray residency |
| Platforms (v1) | macOS (arm64 + amd64), Windows x64 |
| Shell | Tauri 2 |
| UI | Wrap existing admin web UI in app window |
| Backend | Existing Go `grok-bridge` as sidecar |
| Close behavior | Close window → hide to tray (service keeps running) |
| Quit behavior | Explicit Quit from tray/menu stops sidecar and exits |

### Out of scope (v1)

- Rewriting admin UI in React/Next from scratch
- Built-in chat/conversation client
- Linux packaging
- Full code-signing/notarization commercial pipeline
- Multi-instance cluster features

## 3. Architecture

```text
Grok Bridge Desktop (Tauri)
├─ Main window (WebView)
│   └─ loads http://127.0.0.1:<port>/admin
├─ System tray / menu bar
│   ├─ Show window
│   ├─ Start / Stop service
│   ├─ Open in browser (optional helper)
│   ├─ Copy API base URL
│   ├─ Launch at login
│   └─ Quit
└─ Sidecar process: grok-bridge
    ├─ Public API for Claude/Codex
    ├─ Admin API + embedded admin static assets
    └─ SQLite under user data dir
```

### Component responsibilities

| Component | Responsibility |
|-----------|----------------|
| Tauri app | Window lifecycle, tray, autostart, process supervisor, status |
| Go sidecar | All existing business logic (accounts, keys, proxy, logs) |
| Admin UI | Existing SPA under `/admin` |
| User data dir | `config.yaml`, SQLite DB, logs, runtime state |

## 4. Runtime behavior

### Startup sequence

1. App launches (window may start hidden or visible once backend is ready)
2. Resolve user data directory
3. Ensure `config.yaml` exists (seed from example if missing)
4. Pick free local port (or configured fixed port)
5. Start sidecar: `grok-bridge -config <user-config>`
6. Wait until `/healthz` or admin route is healthy
7. Load admin UI in desktop window
8. Show tray icon and keep running

### Window / tray rules

- Click tray → show/focus main window
- Window close button → hide to tray (do not kill sidecar)
- Tray “Quit” → stop sidecar gracefully, then exit app
- Second app instance → focus existing window (single-instance)

### Service control

Tray actions:

- **Start service** (if stopped)
- **Stop service** (keep app alive)
- **Restart service**
- Status indicator in tray tooltip: Running / Stopped / Starting / Error

## 5. Paths and config

### User data directories

- macOS: `~/Library/Application Support/com.grokbridge.desktop/`
- Windows: `%AppData%\com.grokbridge.desktop\`

Contents:

```text
config.yaml
data/grok-bridge.db
logs/desktop.log
logs/sidecar.log
```

### Defaults

- Listen: `127.0.0.1:18080` (desktop default; avoid colliding with other local tools)
- Admin password: require first-run setup if empty/`change-me`
- API for clients remains local HTTP on the chosen port

## 6. Packaging targets

| Platform | Artifact |
|----------|----------|
| macOS arm64 | `Grok Bridge.app` / `.dmg` |
| macOS amd64 | `Grok Bridge.app` / `.dmg` |
| Windows x64 | NSIS or MSI installer + tray app |

Bundle includes:

- Tauri frontend/shell
- Platform-specific `grok-bridge` sidecar binary
- Icons, version metadata

## 7. Implementation plan outline

1. Scaffold `desktop/` Tauri app in repo
2. Integrate tray + main window lifecycle
3. Embed/start Go sidecar with health wait
4. Point window to local admin UI
5. Autostart + single-instance
6. Build scripts for macOS/Windows packages
7. Smoke test: install → open window → create key → proxy request → logs show timings

## 8. Success criteria

- Double-click app opens desktop UI without manual terminal commands
- Backend auto-starts and serves Claude/Codex clients
- Tray keep-alive works on Mac and Windows
- Existing admin features remain usable inside the window
- Portable/release packages can be produced for Mac + Windows x64

## 9. Notes

Wrapping the current web admin is intentional for v1 speed and parity with Codex-Manager’s “desktop shell over local service” approach. A later version may migrate the frontend into Tauri’s frontend dist fully offline without depending on sidecar HTTP for UI assets, but that is not required for v1.
