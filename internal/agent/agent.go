package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/berkaycubuk/polaris-agent/internal/attachment"
	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/session"
	"github.com/berkaycubuk/polaris-agent/internal/skills"
	"github.com/berkaycubuk/polaris-agent/internal/snapshot"
	"github.com/berkaycubuk/polaris-agent/internal/tools"
)

const (
	defaultSoul = `You are Polaris, a personal AI companion that adapts and grows with your user.
You have a persistent identity stored in SOUL.md and you learn about your user over time.
You take notes, do research, and tinker on ideas alongside your user.
You build up knowledge in a personal wiki of markdown files. When you learn something
worth remembering — facts about your user, recurring topics, project context — write it
to a wiki file under wiki/ so future-you can find it. Search the wiki before you assume
you don't know something.
Be candid, curious, and concise.`
)

type Agent struct {
	llm               *llm.Client
	tools             *tools.Registry
	dataDir           string
	maxToolIterations int
	sessions          *session.Store
	processor         *attachment.Processor
	snap              *snapshot.Snapshotter
}

type Options struct {
	MaxToolIterations int
	Processor         *attachment.Processor
	Snapshotter       *snapshot.Snapshotter
}

func New(c *llm.Client, t *tools.Registry, dataDir string, opts Options) *Agent {
	if opts.MaxToolIterations <= 0 {
		opts.MaxToolIterations = 30
	}
	return &Agent{
		llm:               c,
		tools:             t,
		dataDir:           dataDir,
		maxToolIterations: opts.MaxToolIterations,
		sessions:          session.NewStore(),
		processor:         opts.Processor,
		snap:              opts.Snapshotter,
	}
}

// Chat sends a user message into the named session and returns the assistant
// reply. sessionID may be empty for one-off, stateless calls. Image
// attachments are captioned by a separate vision model and (optionally)
// uploaded to R2; only the text caption + storage URL enter session history,
// keeping the context window light.
func (a *Agent) Chat(ctx context.Context, sessionID, userMessage string, attachments ...attachment.Attachment) (string, error) {
	if err := a.ensureDataDirs(); err != nil {
		return "", err
	}

	var history []llm.Message
	if sessionID != "" {
		history = a.sessions.Get(sessionID)
	}

	if len(history) == 0 {
		sys, err := a.buildSystemPrompt()
		if err != nil {
			return "", err
		}
		history = append(history, llm.Message{Role: llm.RoleSystem, Content: sys})
	}

	userMsg, err := a.processor.Compose(ctx, userMessage, attachments)
	if err != nil {
		return "", err
	}
	history = append(history, userMsg)

	specs := a.tools.Specs()

	for i := 0; i < a.maxToolIterations; i++ {
		msg, err := a.llm.Chat(ctx, history, specs)
		if err != nil {
			return "", err
		}
		history = append(history, *msg)

		if len(msg.ToolCalls) == 0 {
			if sessionID != "" {
				a.sessions.Set(sessionID, history)
			}
			// Best-effort: snapshot whatever the agent changed this turn.
			// Snapshot failures must never fail a chat reply.
			_ = a.snap.Commit(sessionID)
			return msg.Content, nil
		}

		toolCtx := session.WithID(ctx, sessionID)
		for _, tc := range msg.ToolCalls {
			result, err := a.tools.Run(toolCtx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("error: %v", err)
			}
			log.Printf("tool %s -> %d bytes", tc.Function.Name, len(result))
			history = append(history, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}
	return "", fmt.Errorf("agent exceeded %d tool iterations", a.maxToolIterations)
}

// Reset clears a session's history.
func (a *Agent) Reset(sessionID string) {
	a.sessions.Delete(sessionID)
}

func (a *Agent) ensureDataDirs() error {
	for _, d := range []string{"", "wiki", "skills"} {
		if err := os.MkdirAll(filepath.Join(a.dataDir, d), 0o755); err != nil {
			return err
		}
	}
	return skills.SeedBuiltins(filepath.Join(a.dataDir, "skills"))
}

func (a *Agent) buildSystemPrompt() (string, error) {
	var b strings.Builder

	soul := readOr(filepath.Join(a.dataDir, "SOUL.md"), defaultSoul)
	b.WriteString(soul)
	b.WriteString("\n\n")

	const userLimit = 1375
	const memoryLimit = 2200

	user := readOr(filepath.Join(a.dataDir, "USER.md"), "")
	fmt.Fprintf(&b, "# What you know about your user (USER.md, %d/%d chars)\n", len(user), userLimit)
	if user != "" {
		b.WriteString(user)
	} else {
		b.WriteString("(empty — fill in lasting facts about your user as you learn them)")
	}
	b.WriteString("\n\n")

	memory := readOr(filepath.Join(a.dataDir, "MEMORY.md"), "")
	fmt.Fprintf(&b, "# Your personal notes (MEMORY.md, %d/%d chars)\n", len(memory), memoryLimit)
	if memory != "" {
		b.WriteString(memory)
	} else {
		b.WriteString("(empty — your scratchpad for things worth remembering across turns)")
	}
	b.WriteString("\n\n")

	b.WriteString("# Managing your memory\n")
	fmt.Fprintf(&b, "USER.md is capped at %d chars; MEMORY.md is capped at %d chars. Both files load into every system prompt, so they must stay tight.\n", userLimit, memoryLimit)
	b.WriteString("Write to them ONLY through the manage_memory tool — write_file is blocked for these paths.\n")
	b.WriteString("Default to action=\"add\" — it appends a new entry without touching existing notes, so you cannot accidentally drop earlier memories. Use action=\"rewrite\" only when you are intentionally consolidating, summarizing, or trimming (e.g. after an over-cap error). Use action=\"view\" if you need a fresh read.\n")
	b.WriteString("Save proactively, don't wait to be asked. When you learn a lasting fact about your user → manage_memory(action=\"add\", scope=\"user\"). When you want to carry a working note across turns (an in-progress idea, an open question, a pattern you noticed about how the user works) → manage_memory(action=\"add\", scope=\"memory\").\n")
	b.WriteString("When an add would exceed the cap, the tool errors. Then either: (a) rewrite the file with older entries summarized, or (b) move older entries into wiki/<topic>.md (your unbounded long-term store), rewrite the hot file with what's left, and retry the add. Use search_wiki to recall what you moved. The cap is a forcing function: stale notes belong in the wiki, not in your hot memory.\n\n")

	skillEntries, err := skills.Load(filepath.Join(a.dataDir, "skills"))
	if err != nil {
		return "", err
	}
	if len(skillEntries) > 0 {
		b.WriteString(skills.FormatSkillList(skillEntries))
	}

	if names := a.tools.SkillEnvNames(); len(names) > 0 {
		b.WriteString("# Skill secrets (env, redacted)\n")
		b.WriteString("Scripts read via os.environ. Values are redacted from tool output, so you'll only see the value as [REDACTED] in any script you run. Treat as opaque secrets — never echo or paste into a reply. Names:\n")
		for _, n := range names {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}
	if names := a.tools.PublicEnvNames(); len(names) > 0 {
		b.WriteString("# Skill identifiers (env, public)\n")
		b.WriteString("Public identifiers (OAuth client_id, public webhook IDs, etc.). Passed to scripts via os.environ AND visible in tool output — safe to include in user-facing URLs (e.g. OAuth auth links). Names:\n")
		for _, n := range names {
			fmt.Fprintf(&b, "- %s\n", n)
		}
		b.WriteString("\n")
	}
	if files := a.tools.SecretsFiles(); len(files) > 0 {
		b.WriteString("# Skill secrets (files)\n")
		b.WriteString("Files under /app/data/secrets/ — scripts open them directly; contents are redacted from tool output:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- secrets/%s\n", f)
		}
		b.WriteString("\n")
	}

	b.WriteString("# Working notes\n")
	fmt.Fprintf(&b, "Data directory: %s\n", a.dataDir)
	b.WriteString("Use search_wiki before answering from memory; write new knowledge into wiki/<topic>.md.\n")
	b.WriteString("Update USER.md when you learn lasting facts about your user. Use MEMORY.md for working notes you want to carry across turns.\n")
	b.WriteString("\n")
	b.WriteString("# Growing your skills\n")
	b.WriteString("Skills under skills/ are how you teach future-you. After a non-trivial task — one that took several steps, a tricky workflow you figured out, or a recurring user need — save the approach as a skill via manage_skill(action=\"create\", ...). When you use a skill and find it outdated, incomplete, or wrong, edit it in the same turn with manage_skill(action=\"edit\", ...) — don't wait to be asked. Read skills/skill-builder.md before authoring. Skills you keep current are leverage; stale skills are liabilities. Archive (don't delete) skills that no longer apply.\n\n")

	b.WriteString("# Scheduling background jobs\n")
	b.WriteString("manage_schedule lets you run a prompt later — once at a specific time, after a duration, or on a recurring interval. Each fired job starts in a fresh session with no chat history, so the prompt must be self-contained.\n")
	b.WriteString("Use it when the user asks for a reminder, a recurring check-in, or any future task. Replies are auto-delivered back to the originating chat (Telegram chats by default; otherwise saved to schedule/output/). You do NOT need a separate \"send\" step — your reply at fire time IS the message.\n")
	b.WriteString("Schedule formats: \"30m\", \"2h\", \"1d\" (one-shot), \"every 30m\" / \"every 2h\" (recurring), or RFC3339 timestamps. Cron expressions are not supported.\n")
	b.WriteString("Cron jobs MUST NOT schedule more cron jobs — the tool blocks recursive scheduling. To stop a job, list to find the id, then remove or pause.\n")

	return b.String(), nil
}

func readOr(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(b))
}
