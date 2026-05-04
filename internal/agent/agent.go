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
}

type Options struct {
	MaxToolIterations int
	Processor         *attachment.Processor
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
			return msg.Content, nil
		}

		for _, tc := range msg.ToolCalls {
			result, err := a.tools.Run(ctx, tc.Function.Name, tc.Function.Arguments)
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

	if user := readOr(filepath.Join(a.dataDir, "USER.md"), ""); user != "" {
		b.WriteString("# What you know about your user\n")
		b.WriteString(user)
		b.WriteString("\n\n")
	}

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
	b.WriteString("Update USER.md when you learn lasting facts about your user.\n")
	b.WriteString("\n")
	b.WriteString("# Growing your skills\n")
	b.WriteString("Skills under skills/ are how you teach future-you. After a non-trivial task — one that took several steps, a tricky workflow you figured out, or a recurring user need — save the approach as a skill via manage_skill(action=\"create\", ...). When you use a skill and find it outdated, incomplete, or wrong, edit it in the same turn with manage_skill(action=\"edit\", ...) — don't wait to be asked. Read skills/skill-builder.md before authoring. Skills you keep current are leverage; stale skills are liabilities. Archive (don't delete) skills that no longer apply.\n")

	return b.String(), nil
}

func readOr(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(b))
}
