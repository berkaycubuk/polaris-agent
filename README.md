# Polaris Agent

A personal AI companion that improves and adapts to its user. Single Go binary,
sandboxed in Docker, configured via `.env`. Knowledge persists as markdown.

See `SPECS.md` for the full design.

## Quick start

```bash
cp .env.example .env
# edit .env (LLM_*, AUTH_TOKEN)
docker compose up --build
```

Talk to it over HTTP:

```bash
curl -s localhost:8080/chat \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "content-type: application/json" \
  -d '{"session":"me","message":"hello"}'
```

Reset a session:

```bash
curl -s -X POST localhost:8080/reset \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -H "content-type: application/json" \
  -d '{"session":"me"}'
```

To enable Telegram, set `TELEGRAM_BOT_TOKEN` in `.env`.

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
