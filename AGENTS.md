# Polaris Agent

A personal AI companion that improves and adapts to its user over time.

## Architecture

Two binaries:

- **`polaris`** — CLI tool, runs on the host machine. Handles setup, diagnostics.
- **`polaris-server`** — Agent server, runs inside Docker. Handles the agent loop, tools, HTTP/Telegram interfaces.

See `SPECS.md` for the full design and `README.md` for usage.

## Development

Before committing changes make sure tests pass:

```bash
go test ./...
```
