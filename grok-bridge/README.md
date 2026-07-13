# grok-bridge

Independent Go proxy: Claude Code / Codex → xAI Grok.

## Quick start

```bash
go test ./...
go run ./cmd/server
curl -s localhost:8080/healthz   # ok
```

Listen address can be overridden with `GROK_BRIDGE_LISTEN` (default `:8080`).

See `config.example.yaml` for upcoming configuration.
