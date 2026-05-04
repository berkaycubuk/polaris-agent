# Polaris Agent Specs

## Overview

Polaris Agent is a personal AI companion that improves and adapts to its
user over time. It has a personality, memories, and a growing knowledge base.

It supports any OpenAI-compatible chat-completions endpoint (OpenAI, Gemini,
Anthropic-compat, Groq, OpenRouter, local Ollama, etc.).

Configured via `.env` file. Two binaries: a CLI tool for the host machine,
and a server that runs sandboxed in Docker.

## Interfaces

### HTTP API

Runs on port 8080 (configurable via `HTTP_ADDR`). All endpoints except
`/healthz` require a `Bearer` token via the `Authorization` header
(matched against `AUTH_TOKEN` using constant-time comparison).

```
POST /chat   — send a message, get a reply
POST /reset  — clear session history
GET  /healthz — unauthenticated health check
```

Request body for `/chat`:
```json
{"session": "me", "message": "hello"}
```

### Telegram (optional)

Enabled by setting `TELEGRAM_BOT_TOKEN`. Supports text and image messages.

**Access control:**
- If `TELEGRAM_ALLOWED_USERS` is set (comma-separated chat IDs), only those
  users can interact with the bot.
- If not set, the **first person to message the bot is auto-claimed as the
  owner**. Their chat ID is persisted to `/app/data/.telegram-owner` and
  survives restarts. All other users are blocked.
- To reset ownership, delete `.telegram-owner` from the data volume and
  restart.

### Image understanding (optional)

A separate vision model (e.g. gemini-2.5-flash-lite) captions image
attachments. Only the text caption enters session history — image bytes
never do. Requires all three `IMAGE_CAPTION_*` env vars.

Original images can optionally be uploaded to Cloudflare R2 for long-term
storage. Requires all four `R2_*` env vars (plus optional `R2_PUBLIC_BASE_URL`).

## Configuration

All config via environment variables (loaded from `.env` if present).
Env vars already set in the environment take precedence over `.env`.

### Required

| Variable | Description |
|----------|-------------|
| `LLM_BASE_URL` | OpenAI-compatible chat-completions endpoint |
| `LLM_MODEL` | Model name (e.g. `gpt-4o-mini`) |
| `LLM_API_KEY` | API key for the LLM provider |
| `AUTH_TOKEN` | Bearer token for HTTP API authentication |

### Optional

| Variable | Default | Description |
|----------|---------|-------------|
| `DATA_DIR` | `/app/data` | Root directory for all persistent data |
| `HTTP_ADDR` | `:8080` | HTTP listen address |
| `MAX_TOOL_ITERATIONS` | `30` | Max tool calls per turn before giving up |
| `TELEGRAM_BOT_TOKEN` | _(empty)_ | Enables Telegram interface when set |
| `TELEGRAM_ALLOWED_USERS` | _(empty)_ | Comma-separated allowed Telegram chat IDs (see access control above) |
| `IMAGE_CAPTION_BASE_URL` | _(empty)_ | Vision model endpoint (set all 3 or none) |
| `IMAGE_CAPTION_MODEL` | _(empty)_ | Vision model name |
| `IMAGE_CAPTION_API_KEY` | _(empty)_ | Vision model API key |
| `R2_ACCOUNT_ID` | _(empty)_ | Cloudflare R2 account (set all 4 or none) |
| `R2_BUCKET` | _(empty)_ | R2 bucket name |
| `R2_ACCESS_KEY_ID` | _(empty)_ | R2 access key |
| `R2_SECRET_ACCESS_KEY` | _(empty)_ | R2 secret key |
| `R2_PUBLIC_BASE_URL` | _(empty)_ | Public URL base for R2 objects |

Related variable groups (`IMAGE_CAPTION_*`, `R2_*`) must be either all set
or all empty. Partial configuration is a startup error.

## Data layout

All persistent state lives in `DATA_DIR` (mounted as a Docker volume at
`/app/data` in production):

```
/app/data/
├── SOUL.md              — agent identity, injected at top of system prompt
├── USER.md              — what the agent knows about the user (1375-char cap)
├── MEMORY.md            — agent's personal cross-turn notes (2200-char cap)
├── .telegram-owner      — auto-detected Telegram owner chat ID
├── wiki/                — agent-grown knowledge base (markdown files)
├── skills/              — agent skills (markdown + optional scripts)
└── secrets/             — user-provided secret files (redacted from output)
```

### SOUL.md

Stores the agent's core identity and personality. Injected at the top of
the system prompt. If missing, a built-in default personality is used.

### USER.md & MEMORY.md (hot memory)

Two short markdown files that load into every system prompt and form the
agent's hot working memory:

- **USER.md** (1375-char cap, ~500 tokens) — lasting facts about the user.
- **MEMORY.md** (2200-char cap, ~800 tokens) — the agent's personal notes
  carried across turns (in-progress ideas, open questions, observed patterns).

Both are written exclusively through the `manage_memory` tool, which
enforces the cap. When a write would exceed the limit, the agent must
summarize older entries or move them into `wiki/<topic>.md` for long-term
storage and retry. The wiki is the unbounded backing store; USER.md and
MEMORY.md are deliberately tight so every turn pays a small fixed cost.

The agent is told to save proactively — not wait to be asked.

### Wiki

Knowledge stored as markdown files following the LLM Wiki principle
(https://gist.githubusercontent.com/karpathy/442a6bf555914893e9891c11519de94f/raw/ac46de1ad27f92b28ac95459c782c07f6b8c964a/llm-wiki.md).

Files are chunked (~500 tokens ≈ ~2000 chars) at paragraph boundaries.
Search uses TF-IDF scoring with stopword filtering. The `search_wiki` tool
returns the top 3 most relevant chunks. No external vector databases.

### Skills

Skills follow the agentskills.io standard. Stored as markdown files in
`skills/`. Skills can be flat files (`skills/foo.md`) or directories
(`skills/foo/SKILL.md`) when they need executable scripts.

The agent can develop its own skills or the user can ask it to load one.
Built-in skills (like `skill-builder.md`) are seeded on first run and not
overwritten if the user edits them.

Scripts use `uv` (https://docs.astral.sh/uv) for Python dependency
management with a shared global cache.

### Secrets

Files placed under `secrets/` are available to skill scripts at runtime.
Their contents are automatically redacted from all tool output before it
reaches the LLM, preventing accidental leaks.

## Built-in tools

### read_file

Read file contents. Paths are relative to the data directory. Path traversal
is blocked — paths escaping the data dir are rejected.

### write_file

Create or overwrite a file. Same path restrictions as `read_file`.

### bash

Execute a shell command inside the container. Runs from the data directory.
Configurable timeout (default 30s, max 300s). Tool output is redacted to
strip any known secret values before returning to the LLM.

### search_wiki

Search the wiki knowledge base. Returns top 3 most relevant chunks using
TF-IDF scoring.

### manage_memory

Read and write the hot-memory files (`USER.md` and `MEMORY.md`). Three
actions:

- **`add`** — append a new entry, preserving existing content. The default
  for new notes; the agent cannot accidentally drop earlier memories.
- **`rewrite`** — replace the whole file. Used only when intentionally
  consolidating, summarizing, or trimming (typically after an over-cap
  error from `add`).
- **`view`** — return current contents and char usage.

Enforces the per-file char cap on every write and rejects overflows,
prompting the agent to summarize via `rewrite` or push older entries
into the wiki.

## Secret redaction

The agent runs a secret redaction system to prevent the LLM from echoing
sensitive values:

- **`SKILL_*` env vars** — passed to subprocesses, redacted from output
- **`SKILL_PUBLIC_*` env vars** — passed to subprocesses, NOT redacted
  (for OAuth client IDs, public webhook URLs, etc.)
- **Files under `secrets/`** — contents redacted from output
- **Parent env vars matching secret patterns** (KEY, TOKEN, SECRET,
  PASSWORD, etc.) — redacted from output, NOT passed to subprocesses

Values under 12 characters are not redacted (too many false positives).
Redacted values are replaced with `[REDACTED]`.

## Sessions

Chat histories are held in memory, keyed by session ID. Session IDs are
arbitrary strings — the HTTP API passes them directly, Telegram uses
`telegram:<chat_id>`. Sessions are lost on container restart.

## System prompt construction

On each new session, the system prompt is built from:

1. `SOUL.md` (or built-in default)
2. `USER.md` (with current/cap char usage)
3. `MEMORY.md` (with current/cap char usage)
4. Memory budget instructions (caps, manage_memory routing, wiki overflow)
5. Loaded skills list (name + description + file path)
6. Available secret/public env var names (values never included)
7. Available secrets files
8. Working notes (data directory path, wiki usage instructions)

## Architecture

Two binaries built from the same Go module:

- **`polaris`** (`cmd/polaris/`) — CLI tool, runs on the host machine.
  Handles `setup`, `doctor`, `chat`, `help`, `version` commands.
  Installed via `go install` or as a standalone binary.

- **`polaris-server`** (`cmd/server/`) — Agent server, runs inside Docker.
  Handles the agent loop, HTTP API, Telegram bot, tools, and data seeding.
  Built inside a multi-stage Docker container.

```
cmd/
├── polaris/main.go       — CLI (setup, doctor, help, version)
└── server/main.go        — server entrypoint, wires everything together

internal/
├── agent/                 — core agent loop (chat, tool calling, system prompt)
├── attachment/            — image processing pipeline (caption + R2 upload)
├── captioner/             — vision model captioning
├── chat/                  — interactive terminal chat client (used by CLI)
├── config/                — env loading, validation, .env parsing
├── doctor/                — diagnostic checks (used by CLI)
├── llm/                   — OpenAI-compatible chat completions client
├── server/                — HTTP API (chat, reset, healthz)
├── session/               — in-memory session store
├── setup/                 — interactive setup wizard (used by CLI)
├── skills/                — skill loading, parsing, built-in seeding
├── storage/               — Cloudflare R2 uploads (S3-compatible SigV4)
├── telegram/              — Telegram bot (long polling, auth)
├── tools/                 — built-in tools + secret redaction
└── wiki/                  — wiki chunking and TF-IDF search
```

The project has **zero external dependencies** — pure Go standard library.

## Tech

- Two Go binaries (CLI + server), zero external dependencies
- Server built inside a multi-stage Docker container
- Alpine-based runtime image, runs as non-root user `polaris`
- Markdown for all persistent data and knowledge
- Docker provides sandboxing for the `bash` tool
- No databases, no vector stores — files only

## Use cases

Users interact with Polaris to take notes, do research, and tinker on ideas.
The agent learns more about its user over time thanks to the growing wiki
and adaptive USER.md.
