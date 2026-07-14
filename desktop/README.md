# Grok Bridge Desktop

Codex-Manager-style desktop shell for grok-bridge.

## Dev

```bash
# build Go sidecar first
cd ../grok-bridge && go build -o grok-bridge ./cmd/server

# run desktop app
cd ../desktop
npm install
npm run dev
```

## Notes

- Window hosts the existing admin UI
- Tray keeps the app alive
- Close window hides to tray; Quit stops backend
