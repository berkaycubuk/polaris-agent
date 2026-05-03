# Polaris Agent Specs

## Overview

Polaris Agent is a personal AI companion that improves and adapts to its
user. It has a personality and memories.

It supports OpenAI compatible LLM providers.

It is configured with .env file.

```
LLM_BASE_URL=...
LLM_MODEL=....
LLM_API_KEY=sk-.....

TELEGRAM_BOT_TOKEN=.... # optional, add it to enable Telegram

AUTH_TOKEN=.... # user fills it, used for authentication
```

`USER.md` file stores user preferences, communication style.
`SOUL.md` file stores the agent's core identity and is injected
at top in the system prompt.
Rest of the collected data and knowledge stored with llm wiki principle.
LLM Wiki is defined here: https://gist.githubusercontent.com/karpathy/442a6bf555914893e9891c11519de94f/raw/ac46de1ad27f92b28ac95459c782c07f6b8c964a/llm-wiki.md

All data (USER.md, SOUL.md, wiki, skills) stored in `/app/data`. This directory must be mounted as a
Docker volume.

Agent can develop it's own skills as it sees a need, or the user can
ask the agent to load a skill from publicly available source.

Skills are stored as standard markdown files in /app/data/skills/.
Skill standards are defined here: https://agentskills.io/

Tools built-in to the agent:

### read_file

Allows the agent to read file contents.

### write_file

Allows the agent to write to files.

### bash

Allows the agent to execute bash commands.

### search_wiki

Allows the agent to search it's wiki knowledge base. Fetches top 3 relevant
results. Implementation: split wiki markdown files into chunks (~500 tokens).
Uses basic keyword matching to find and return the 3 most relevant
chunks. Do NOT use external vector databases.

Authentication is handled via `AUTH_TOKEN` env variable. User have to
rotate the token himself to secure the agent.

## Use cases

Users use this agent to take notes, do researches and tinker on ideas.
Agent will learn more about it's user over time thanks to growing llm wiki.

## Tech

Single Golang binary that lives inside a docker container. Uses markdown
to store knowledge and data. Docker should sandbox the agent to secure
the computer the agent runs.
