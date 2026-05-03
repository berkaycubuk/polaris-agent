package agent

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/captioner"
	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/storage"
	"github.com/berkaycubuk/polaris-agent/internal/tools"
)

//go:embed builtins/*.md
var builtinSkillsFS embed.FS

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
	mu                sync.Mutex
	llm               *llm.Client
	tools             *tools.Registry
	dataDir           string
	maxToolIterations int
	captioner         *captioner.Captioner // optional
	r2                *storage.R2          // optional
	sessions          map[string][]llm.Message
}

type Options struct {
	MaxToolIterations int
	Captioner         *captioner.Captioner
	R2                *storage.R2
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
		captioner:         opts.Captioner,
		r2:                opts.R2,
		sessions:          map[string][]llm.Message{},
	}
}

// Attachment is binary content (e.g. an image) attached to a user message.
type Attachment struct {
	Data     []byte
	MimeType string // e.g. "image/jpeg"
}

// Chat sends a user message into the named session and returns the assistant
// reply. sessionID may be empty for one-off, stateless calls. Image
// attachments are captioned by a separate vision model and (optionally)
// uploaded to R2; only the text caption + storage URL enter session history,
// keeping the context window light.
func (a *Agent) Chat(ctx context.Context, sessionID, userMessage string, attachments ...Attachment) (string, error) {
	if err := a.ensureDataDirs(); err != nil {
		return "", err
	}

	var history []llm.Message
	if sessionID != "" {
		a.mu.Lock()
		history = append([]llm.Message(nil), a.sessions[sessionID]...)
		a.mu.Unlock()
	}

	if len(history) == 0 {
		sys, err := a.buildSystemPrompt()
		if err != nil {
			return "", err
		}
		history = append(history, llm.Message{Role: llm.RoleSystem, Content: sys})
	}

	userMsg, err := a.composeUserMessage(ctx, userMessage, attachments)
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
				a.mu.Lock()
				a.sessions[sessionID] = history
				a.mu.Unlock()
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
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, sessionID)
}

func (a *Agent) ensureDataDirs() error {
	for _, d := range []string{"", "wiki", "skills"} {
		if err := os.MkdirAll(filepath.Join(a.dataDir, d), 0o755); err != nil {
			return err
		}
	}
	return a.seedBuiltinSkills()
}

// seedBuiltinSkills writes embedded built-in skills into skills/ if they
// are missing. It does NOT overwrite existing files, so the user (or the
// agent itself) can edit a skill without it being clobbered on restart.
func (a *Agent) seedBuiltinSkills() error {
	entries, err := builtinSkillsFS.ReadDir("builtins")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		dst := filepath.Join(a.dataDir, "skills", e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := builtinSkillsFS.ReadFile("builtins/" + e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		log.Printf("seeded built-in skill: %s", e.Name())
	}
	return nil
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

	skills, err := loadSkills(filepath.Join(a.dataDir, "skills"))
	if err != nil {
		return "", err
	}
	if len(skills) > 0 {
		b.WriteString("# Skills available\n")
		b.WriteString("You have access to the following skills. Read the corresponding file when one is relevant.\n\n")
		for _, s := range skills {
			fmt.Fprintf(&b, "- %s — %s (file: skills/%s)\n", s.Name, s.Description, s.File)
		}
		b.WriteString("\n")
	}

	b.WriteString("# Working notes\n")
	b.WriteString(fmt.Sprintf("Data directory: %s\n", a.dataDir))
	b.WriteString("Use search_wiki before answering from memory; write new knowledge into wiki/<topic>.md.\n")
	b.WriteString("Update USER.md when you learn lasting facts about your user.\n")

	return b.String(), nil
}

type skillEntry struct {
	Name        string
	Description string
	File        string
}

// loadSkills reads agentskills.io-style markdown files. We extract the optional
// frontmatter `name` and `description` fields; if absent, we fall back to the
// filename and the first non-empty line.
func loadSkills(dir string) ([]skillEntry, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []skillEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		name, desc := parseSkillFrontmatter(string(data))
		if name == "" {
			name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		if desc == "" {
			desc = firstNonEmptyLine(string(data))
		}
		out = append(out, skillEntry{Name: name, Description: desc, File: e.Name()})
	}
	return out, nil
}

func parseSkillFrontmatter(s string) (name, desc string) {
	if !strings.HasPrefix(s, "---") {
		return "", ""
	}
	rest := strings.TrimPrefix(s, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	fm := rest[:end]
	for _, line := range strings.Split(fm, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
			name = strings.Trim(name, `"'`)
		} else if strings.HasPrefix(line, "description:") {
			desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			desc = strings.Trim(desc, `"'`)
		}
	}
	return
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") && line != "---" {
			return line
		}
	}
	return ""
}

// composeUserMessage processes attachments into a single text-only user
// message. For each image: optionally upload to R2 (preserving the original)
// and caption via the vision model; the caption + storage URL are inlined
// into the message text. Image bytes never enter session history.
func (a *Agent) composeUserMessage(ctx context.Context, text string, atts []Attachment) (llm.Message, error) {
	if len(atts) == 0 {
		return llm.Message{Role: llm.RoleUser, Content: text}, nil
	}

	var b strings.Builder
	if t := strings.TrimSpace(text); t != "" {
		b.WriteString(t)
		b.WriteString("\n\n")
	}

	for i, att := range atts {
		mime := att.MimeType
		if mime == "" {
			mime = "image/jpeg"
		}

		var storedURL string
		if a.r2 != nil {
			key := imageKey(mime)
			url, err := a.r2.Put(ctx, key, att.Data, mime)
			if err != nil {
				log.Printf("r2 put failed: %v", err)
			} else {
				storedURL = url
			}
		}

		var caption string
		if a.captioner != nil {
			c, err := a.captioner.Caption(ctx, att.Data, mime, text)
			if err != nil {
				log.Printf("caption failed: %v", err)
				caption = "(caption unavailable: " + err.Error() + ")"
			} else {
				caption = c
			}
		} else {
			caption = "(no captioner configured; image content not described)"
		}

		fmt.Fprintf(&b, "[Image %d — %s]\n%s\n", i+1, mime, caption)
		if storedURL != "" {
			fmt.Fprintf(&b, "Stored at: %s\n", storedURL)
		}
		if i < len(atts)-1 {
			b.WriteString("\n")
		}
	}

	return llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(b.String())}, nil
}

// imageKey produces a sortable, collision-resistant object key for R2.
// Format: images/YYYY/MM/<unix>-<rand>.<ext>
func imageKey(mime string) string {
	now := time.Now().UTC()
	var rb [6]byte
	_, _ = rand.Read(rb[:])
	ext := extFromMime(mime)
	return fmt.Sprintf("images/%04d/%02d/%d-%s%s",
		now.Year(), int(now.Month()), now.Unix(), hex.EncodeToString(rb[:]), ext)
}

func extFromMime(m string) string {
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".bin"
}

func readOr(path, fallback string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(b))
}
