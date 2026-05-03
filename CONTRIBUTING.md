# Contributing to Polaris Agent

## Prerequisites

- [Go](https://go.dev/dl/) 1.24+
- [Docker](https://docs.docker.com/get-docker/) with Docker Compose

## Development

Clone and run the tests:

```bash
git clone https://github.com/berkaycubuk/polaris-agent.git
cd polaris-agent
go test ./...
```

Build the CLI:

```bash
go build -o polaris ./cmd/polaris
```

Build the server (inside Docker):

```bash
docker compose build
```

## Project layout

```
cmd/
├── polaris/     CLI tool (runs on host)
└── server/      Agent server (runs in Docker)

internal/
├── agent/        Core agent loop (chat, tool calling, system prompt)
├── attachment/   Image processing pipeline
├── captioner/    Vision model captioning
├── config/       Env loading, .env parsing
├── doctor/       Diagnostic checks (CLI)
├── llm/          OpenAI-compatible chat client
├── server/       HTTP API (chat, reset, healthz)
├── session/      In-memory session store
├── setup/        Interactive setup wizard (CLI)
├── skills/       Skill loading and built-in seeding
├── storage/      Cloudflare R2 uploads
├── telegram/     Telegram bot
├── tools/        Built-in tools + secret redaction
└── wiki/         Wiki chunking and TF-IDF search
```

## Making changes

1. Create a branch for your change.
2. Write your code. Follow the existing style — pure Go standard library, no external dependencies.
3. Add or update tests. All packages with logic have test files.
4. Run the full test suite: `go test ./...`
5. If changing the server, verify it builds: `docker compose build`
6. Open a pull request with a clear description.

## Guidelines

- **Zero external dependencies.** The project uses only the Go standard library. Do not add third-party packages.
- **Tests required.** New packages should include `_test.go` files. Bug fixes should include regression tests.
- **Keep it simple.** No databases, no vector stores, no frameworks. Files and the standard library.
- **Secret safety.** If your change touches tool output, make sure it goes through the secret redaction system.

## Reporting issues

Open a GitHub issue with:

- What you expected to happen
- What actually happened
- Steps to reproduce (including your `.env` config with secrets redacted)
- `polaris doctor` output (also redact secrets)

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
