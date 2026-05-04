---
name: skill-builder
description: How to author a new Polaris skill. Use this whenever the user asks for a new capability, workflow, or recurring task that should persist as a reusable skill.
---

# Skill Builder

You (Polaris) author your own skills. A skill is a single markdown file in
`skills/` that teaches future-you how to handle a specific task or workflow.
Once a skill exists, you don't have to re-derive the approach from scratch
every time — you just read the file and follow it.

This skill exists so you never have to re-learn the skills system itself.

## When to create a skill

Create one when the user asks for a recurring workflow, a domain you'll
revisit ("manage my groceries", "track my reading", "summarize my week"),
or any non-trivial capability they'll want again. **Do not** create skills
for one-off questions, trivia, or things that fit naturally in the wiki as
plain notes.

If you're unsure, ask the user one short question: "Do you want this as a
reusable skill, or a one-off?"

## File location and naming

A skill is **either** a flat markdown file or a directory, depending on
whether it needs executable code. Default to flat — most skills do.

- **Markdown-only skill** (the default):
  `skills/<kebab-case-name>.md` (e.g. `skills/shopping-list.md`).
- **Skill with scripts** (only when the workflow genuinely needs code —
  external API auth flows, data munging that's painful in shell, anything
  reusable enough to deserve its own venv):
  `skills/<kebab-case-name>/SKILL.md` plus the script files alongside it.

Use kebab-case names. One skill per file (or directory). Skill data
(lists, logs, state) lives in `wiki/` or a dedicated subfolder the skill
defines — never inline in the skill file.

## When a skill needs scripts

You have `uv` (https://docs.astral.sh/uv) available in the container.
`uv` manages Python environments and dependencies. Cache lives at
`/app/data/.uv-cache` and is shared across skills via hardlinks, so a
package installed in two skills costs disk space once.

Two layouts, pick the simpler one that fits:

**A. Single-file script with inline deps (PEP 723).** Best for skills
with one script and a few dependencies. No directory, no `pyproject.toml`,
no manual venv. Put the deps in a header comment; `uv run` builds an
ephemeral venv on first run and caches it.

```python
# /// script
# requires-python = ">=3.12"
# dependencies = ["ytmusicapi"]
# ///
import sys
from ytmusicapi import YTMusic
...
```

Layout:
```
skills/youtube-music.md          # frontmatter + how to use
skills/youtube-music.py          # the PEP 723 script
```
Run with: `uv run skills/youtube-music.py <args>`

**B. Directory layout with `pyproject.toml`.** Use when a skill has
multiple scripts, helper modules, or non-trivial configuration. `uv`
creates `.venv/` on first run; subsequent runs are instant.

Layout:
```
skills/<name>/
  SKILL.md              # the skill spec
  pyproject.toml        # declared dependencies
  main.py               # entrypoint(s)
  helpers.py            # optional helper modules
  .venv/                # uv-managed, do not commit logically
```
Setup (once, when authoring):
```bash
cd /app/data/skills/<name>
uv init --bare --no-workspace --python 3.12
uv add ytmusicapi   # add each dep the skill needs
```
Run later: `cd /app/data/skills/<name> && uv run python main.py <args>`

In both layouts, `uv` re-uses the global cache, so adding a package
already used by another skill is fast and nearly free on disk.

## Secrets

Never paste API tokens, OAuth refresh tokens, or passwords into a script
file. The user supplies secrets two ways; both reach scripts at runtime
without entering your context:

**1. Environment variables in `.env`, two prefixes:**

- `SKILL_*` — actual secrets (refresh tokens, client_secrets, API
  keys). Passed to subprocesses **and redacted** from tool output. You
  will only ever see the value as `[REDACTED]` in any script you run.
  Treat as opaque; never echo into a reply.

- `SKILL_PUBLIC_*` — public identifiers that the user-facing flow
  legitimately needs to surface (OAuth `client_id`, public webhook IDs,
  Stripe publishable keys, etc.). Passed to subprocesses **and not
  redacted**. Safe to include in URLs you send back to the user.

OAuth example (note which prefix to use):

```
SKILL_PUBLIC_GOOGLE_CLIENT_ID=1234567890-abc.apps.googleusercontent.com
SKILL_GOOGLE_CLIENT_SECRET=GOCSPX-...
```

```python
import os
client_id     = os.environ["SKILL_PUBLIC_GOOGLE_CLIENT_ID"]   # visible
client_secret = os.environ["SKILL_GOOGLE_CLIENT_SECRET"]      # secret
```

If you build an auth URL, the `client_id` must be included verbatim
(that's what the OAuth provider expects). The `client_secret` only goes
into server-side token-exchange POSTs, never into URLs.

**2. Files under `/app/data/secrets/`.**
For multi-line blobs (Google service account JSON, PEM keys, OAuth
state), the user writes a file like `/app/data/secrets/google-oauth.json`
out-of-band. Scripts open it directly:

```python
import json, pathlib
creds = json.loads(pathlib.Path("/app/data/secrets/google-oauth.json").read_text())
```

Polaris registers the contents of every file under `secrets/` for
redaction, so even if the agent (you) `read_file`s or `cat`s one, the
sensitive values come back as `[REDACTED]`.

### Rules for skills that need secrets

- The skill's `SKILL.md` must list the env vars and/or files it needs,
  named explicitly (e.g. "requires `SKILL_SPOTIFY_REFRESH_TOKEN`").
- If a required secret is missing, the script fails fast with a clear
  error message naming what's needed — do not improvise placeholders.
- Never log a secret value. Never include one in a tool argument
  (e.g. `bash: echo $SKILL_FOO`). The redactor will catch most leaks but
  the right approach is to not echo them in the first place.
- Don't ask the user to paste a secret into chat. Direct them to add it
  to `.env` or write the file under `secrets/`.

## Required structure

Every skill file MUST start with YAML frontmatter:

```markdown
---
name: <kebab-case-name>
description: <one sentence — when to use this skill, written so future-you can decide relevance at a glance>
---

# <Human-readable title>

<Body — see template below.>
```

The `description` is critical: it's the only thing surfaced in the skills
index inside the system prompt. Make it specific about *triggers* ("when
the user mentions groceries, meal planning, or shopping"), not just the
topic.

## Recommended body sections

Use these as a checklist, not a rigid template — drop sections that don't
apply.

1. **Purpose** — one paragraph. What problem this skill solves for the user.
2. **When to use** — concrete triggers / phrases / situations.
3. **Data layout** — exactly which files this skill reads and writes
   (paths, formats). Be precise; future-you will trust this verbatim.
4. **Operations** — the things the skill knows how to do, each with:
   - the command/intent that triggers it,
   - the steps (read X → mutate → write Y),
   - the reply format.
5. **Conventions** — formatting rules, ordering, defaults, units.
6. **Edge cases** — what to do when the data file doesn't exist, when the
   user is ambiguous, when an item is duplicated, etc.
7. **Examples** — at least one realistic user message → response trace.

## Authoring procedure

When the user asks for a new skill — or when you finish a non-trivial task
worth preserving — follow these steps:

1. **Clarify scope** in one short turn if the request is broad. Ask only
   what you can't reasonably assume (e.g. "one shared list, or per-store?").
   Never ask more than two questions before drafting.
2. **Check what exists.** Use `bash` to list `skills/` and `wiki/` so you
   don't duplicate or collide with existing files.
3. **Pick a name.** Kebab-case, specific. Avoid generic names like
   `notes.md` or `helper.md`.
4. **Decide the data layout** before writing the skill. Where does state
   live? What's the file format? Markdown checkboxes, a table, JSON in a
   fenced block, plain bullets? Pick the simplest option that round-trips
   cleanly when you read and rewrite it.
5. **Decide if the skill needs a script.** Default: no. A skill needs
   scripts only if the workflow genuinely cannot be done with `bash` +
   `read_file` + `write_file` (typically: OAuth flows, paginated API
   clients, data parsing that would be ugly in shell). If yes, pick the
   single-file PEP 723 layout for one script, the directory layout
   otherwise.
6. **Write the skill via `manage_skill`** — pass the body without
   frontmatter; the tool generates it from `path` + `description`. Use
   `manage_skill(action="create", path="<name>.md", ...)` for flat skills,
   or `path="<name>/SKILL.md"` for directory skills (then write any scripts
   alongside with `write_file` and run the `uv` setup once). To revise a
   skill in place use `manage_skill(action="edit", ...)`. To retire one
   use `manage_skill(action="archive", ...)` — this moves it to
   `skills/.archive/`, never deletes. Reach for plain `write_file` only
   for non-skill files (scripts, wiki entries, data).
7. **Seed any data files** the skill expects (e.g. an empty
   `wiki/shopping-list.md` with the right header), so the first real use
   doesn't fail on a missing file.
8. **Update USER.md** if the skill reflects a lasting fact about the user
   ("user keeps a single weekly grocery list", "user prefers metric").
9. **Confirm with the user.** Tell them the skill name, what it does, and
   the trigger phrase that activates it. Offer to demo it.

## Template

```markdown
---
name: <kebab-case-name>
description: <triggers + purpose, one sentence>
---

# <Title>

## Purpose
<one paragraph>

## When to use
- <trigger 1>
- <trigger 2>

## Data layout
- `wiki/<file>.md` — <what's in it, format>

## Operations

### <verb the operation>
Trigger: <user phrasing>
Steps:
1. <read>
2. <mutate>
3. <write>
Reply: <what to tell the user>

## Conventions
- <rule>

## Edge cases
- <case → behavior>

## Example
> User: <message>
> Polaris: <ideal response>
```

## Anti-patterns

- Don't write a skill that just restates SOUL.md — skills are *specific*.
- Don't embed long static data in the skill file; put it in `wiki/`.
- Don't write skills that need external services Polaris can't reach.
- Don't author overlapping skills — if a new request fits an existing
  skill, extend that file instead of creating a sibling.
- Don't skip the frontmatter. Without it, the skill won't surface in the
  system prompt index correctly.
- Don't reach for a script when the workflow could be `bash` + a few
  file ops. Scripts earn their venv by saving real complexity, not by
  being slightly nicer.
- Don't `pip install` outside `uv` (no `pip install --user`, no system
  pip). Always `uv add` (directory layout) or PEP 723 inline deps. This
  keeps installs cached and skills self-contained.
- Don't hard-code secrets in scripts. Read them from
  `/app/data/secrets/<service>.{json,env}` at runtime.

## After authoring

Mention the new skill in your reply so the user knows it exists and how
to invoke it. If the skill produced any user-facing artifact (a starter
list, a template), point at it.
