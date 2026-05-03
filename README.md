# Polaris Agent

A personal AI companion that improves and adapts to its user.
Runs inside Docker for security. Knowledge persists as markdown.

See `SPECS.md` for the full design.

## Requirements

- [Docker](https://docs.docker.com/get-docker/) with Docker Compose
- [Go](https://go.dev/dl/) 1.24+ (to build the CLI from source)

## Quick start

```bash
git clone https://github.com/berkaycubuk/polaris-agent.git
cd polaris-agent

# Build and install the CLI
go install ./cmd/polaris

# Configure the agent
polaris setup

# Start the agent server (Docker)
docker compose up -d
```

**Diagnose issues:**

```bash
polaris doctor
```

Talk to it over HTTP:

```bash
curl -s localhost:8080/chat \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "content-type: application/json" \
  -d '{"session":"me","message":"hello"}'
```

Or use the built-in chat interface:

```bash
polaris chat
```

Reset a session:

```bash
curl -s -X POST localhost:8080/reset \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "content-type: application/json" \
  -d '{"session":"me"}'
```

To enable Telegram, set `TELEGRAM_BOT_TOKEN` in `.env`.

## CLI commands

The `polaris` CLI runs on your machine and manages configuration:

```
polaris chat      Chat with your agent (interactive)
polaris setup     Configure the agent (interactive wizard)
polaris doctor    Diagnose configuration issues
polaris version   Show version
polaris help      Show help
```

The agent server runs inside Docker:

```bash
docker compose up -d      # start the agent
docker compose logs -f    # view logs
docker compose down        # stop the agent
```

### Chat options

```
--server <url>    Server URL (default: http://localhost:8080)
--session <id>    Session ID (default: cli)
--token <token>   Auth token (reads from .env by default)
```

Chat commands (inside the chat):

```
/reset   Clear session history
/quit    Exit the chat
/help    Show available commands
```

## Data layout

All persistent state lives in `/app/data` (Docker volume):

- `SOUL.md` — agent's identity, injected at top of system prompt.
- `USER.md` — user preferences and communication style.
- `wiki/*.md` — agent-grown knowledge base (chunked + keyword-searched via `search_wiki`).
- `skills/*.md` — agent skills following [agentskills.io](https://agentskills.io/).

## Built-in tools

- `read_file` — read a file under the data dir.
- `write_file` — create or overwrite a file under the data dir.
- `bash` — execute a shell command (sandboxed by the container).
- `search_wiki` — keyword search across the wiki, returns top 3 chunks.
