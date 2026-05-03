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

- Path: `skills/<kebab-case-name>.md` (e.g. `skills/shopping-list.md`).
- One skill per file. Keep names short, specific, and verb- or noun-led.
- Skill data (lists, logs, state) lives in `wiki/` or a dedicated subfolder
  the skill defines — never inline in the skill file.

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

When the user asks for a new skill, follow these steps:

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
5. **Draft the skill file** following the structure above. Write it to
   `skills/<name>.md` with `write_file`.
6. **Seed any data files** the skill expects (e.g. an empty
   `wiki/shopping-list.md` with the right header), so the first real use
   doesn't fail on a missing file.
7. **Update USER.md** if the skill reflects a lasting fact about the user
   ("user keeps a single weekly grocery list", "user prefers metric").
8. **Confirm with the user.** Tell them the skill name, what it does, and
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

## After authoring

Mention the new skill in your reply so the user knows it exists and how
to invoke it. If the skill produced any user-facing artifact (a starter
list, a template), point at it.
