# Grok Bridge Desktop App Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Codex-Manager-style Tauri desktop app that hosts the existing grok-bridge admin UI in a native window, keeps a tray process alive, and auto-runs the Go sidecar on macOS + Windows x64.

**Architecture:** Tauri 2 shell supervises `grok-bridge` sidecar, opens WebView to `http://127.0.0.1:<port>/admin`, and manages tray/autostart/single-instance. Business logic stays in Go.

**Tech Stack:** Tauri 2, Rust, existing Go server, current admin SPA assets

**Spec:** `docs/superpowers/specs/2026-07-14-desktop-app-design.md`

## Global Constraints

- Do not rewrite proxy/account/auth logic in Rust
- Reuse existing admin UI for v1
- Close-to-tray by default
- User data outside install directory
- Target: macOS arm64/amd64 + Windows x64

---

### Task 1: Scaffold desktop app workspace

**Files:**
- Create: `desktop/package.json`
- Create: `desktop/src-tauri/` Tauri project
- Create: `desktop/README.md`

- [ ] **Step 1: Create Tauri app skeleton under `desktop/`**
- [ ] **Step 2: Set product name `Grok Bridge`, identifier `com.grokbridge.desktop`**
- [ ] **Step 3: Commit**

### Task 2: Window + tray shell

**Files:**
- Modify: `desktop/src-tauri/src/lib.rs`
- Create: tray/window helpers

- [ ] **Step 1: Main window loads local admin URL when backend ready**
- [ ] **Step 2: Tray menu: Show / Start / Stop / Quit**
- [ ] **Step 3: Close window hides to tray**
- [ ] **Step 4: Commit**

### Task 3: Sidecar process supervisor

**Files:**
- Create: sidecar manager module
- Bundle: platform `grok-bridge` binaries

- [ ] **Step 1: Resolve user data dir and seed config**
- [ ] **Step 2: Spawn sidecar with config path + port**
- [ ] **Step 3: Health-wait then show window**
- [ ] **Step 4: Graceful stop on Quit**
- [ ] **Step 5: Commit**

### Task 4: Desktop UX extras

- [ ] **Step 1: Single-instance focus existing window**
- [ ] **Step 2: Launch-at-login toggle**
- [ ] **Step 3: Tray tooltip status**
- [ ] **Step 4: Commit**

### Task 5: Packaging

- [ ] **Step 1: Build macOS app/dmg scripts**
- [ ] **Step 2: Build Windows x64 installer script**
- [ ] **Step 3: Document install/run**
- [ ] **Step 4: Commit + release artifacts**

### Task 6: Verification

- [ ] Launch app → window shows admin UI
- [ ] Create API key in window
- [ ] Claude/Codex request via local port works
- [ ] Close window keeps service; Quit stops service
