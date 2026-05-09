package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/scheduler"
	"github.com/berkaycubuk/polaris-agent/internal/wiki"
)

// Tool is a built-in agent tool.
type Tool interface {
	Name() string
	Spec() llm.Tool
	Run(ctx context.Context, args string) (string, error)
}

// Registry holds the active tools and the data directory they operate within.
type Registry struct {
	DataDir  string
	tools    map[string]Tool
	redactor *secretRedactor
}

func NewRegistry(dataDir string) *Registry {
	redactor := newSecretRedactor(filepath.Join(dataDir, "secrets"))
	r := &Registry{
		DataDir:  dataDir,
		tools:    map[string]Tool{},
		redactor: redactor,
	}
	r.register(&readFile{dataDir: dataDir})
	r.register(&writeFile{dataDir: dataDir})
	r.register(&bashTool{dataDir: dataDir, redactor: redactor})
	r.register(&searchWiki{dataDir: dataDir})
	r.register(&manageSkill{dataDir: dataDir})
	r.register(&manageMemory{dataDir: dataDir})
	return r
}

func (r *Registry) register(t Tool) { r.tools[t.Name()] = t }

// EnableScheduler registers the manage_schedule tool with the given store
// and firer. Called by cmd/server after the scheduler is constructed,
// since the scheduler depends on the agent which depends on this registry.
func (r *Registry) EnableScheduler(store *scheduler.Store, firer scheduleFirer) {
	r.register(&manageSchedule{store: store, sched: firer, dataDir: r.DataDir})
}

// ChildEnv exposes the redacted child environment so callers (e.g. the
// scheduler's script runner) can launch subprocesses with the same secret
// hygiene the bash tool uses.
func (r *Registry) ChildEnv() []string {
	return r.redactor.ChildEnv()
}

// SkillEnvNames returns the names of secret SKILL_* env vars detected at
// startup, sorted. Values stay in subprocess env and are redacted from
// tool output.
func (r *Registry) SkillEnvNames() []string {
	return r.redactor.SkillEnvNames()
}

// PublicEnvNames returns the names of SKILL_PUBLIC_* env vars — exported
// to subprocesses but NOT redacted (e.g. OAuth client_id).
func (r *Registry) PublicEnvNames() []string {
	return r.redactor.PublicEnvNames()
}

// SecretsFiles returns relative paths under <dataDir>/secrets/ detected
// at startup, sorted.
func (r *Registry) SecretsFiles() []string {
	return r.redactor.SecretsFiles()
}

func (r *Registry) Specs() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t.Spec())
	}
	return out
}

func (r *Registry) Run(ctx context.Context, name, args string) (string, error) {
	t, ok := r.tools[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	out, err := t.Run(ctx, args)
	if err != nil {
		return "", err
	}
	return r.redactor.Redact(out), nil
}

// memoryFiles must be written through manage_memory, not write_file. The
// dedicated tool enforces char caps and treats them as the agent's hot
// working memory rather than free-form files.
var memoryFiles = map[string]bool{
	"MEMORY.md": true,
	"USER.md":   true,
}

// resolvePath joins relative paths under DataDir and prevents escape.
// Absolute paths are allowed only if they remain inside DataDir.
func resolvePath(dataDir, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dataDir, p)
	}
	abs = filepath.Clean(abs)
	rootAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) && abs != rootAbs {
		return "", fmt.Errorf("path escapes data dir: %s", p)
	}
	return abs, nil
}

// ---- read_file ----

type readFile struct{ dataDir string }

func (t *readFile) Name() string { return "read_file" }
func (t *readFile) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name:        "read_file",
			Description: "Read the contents of a file. Paths are relative to the data directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "Path to the file."},
				},
				"required": []string{"path"},
			},
		},
	}
}
func (t *readFile) Run(_ context.Context, args string) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", err
	}
	p, err := resolvePath(t.dataDir, a.Path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- write_file ----

type writeFile struct{ dataDir string }

func (t *writeFile) Name() string { return "write_file" }
func (t *writeFile) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name:        "write_file",
			Description: "Write contents to a file (creates or overwrites). Paths are relative to the data directory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Path to the file."},
					"content": map[string]any{"type": "string", "description": "File contents."},
				},
				"required": []string{"path", "content"},
			},
		},
	}
}
func (t *writeFile) Run(_ context.Context, args string) (string, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", err
	}
	p, err := resolvePath(t.dataDir, a.Path)
	if err != nil {
		return "", err
	}
	if memoryFiles[filepath.Clean(a.Path)] {
		return "", fmt.Errorf("%s is managed by the manage_memory tool, not write_file — call manage_memory(scope=%q, content=...) instead", a.Path, strings.TrimSuffix(strings.ToLower(filepath.Clean(a.Path)), ".md"))
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(a.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len(a.Content), a.Path), nil
}

// ---- bash ----

type bashTool struct {
	dataDir  string
	redactor *secretRedactor
}

func (t *bashTool) Name() string { return "bash" }
func (t *bashTool) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name: "bash",
			Description: "Execute a bash command inside the agent's container. Runs from the data directory by default. " +
				"Refuses obviously catastrophic commands (rm -rf /, find -delete, mkfs, etc.) and refuses to mutate " +
				"SOUL.md, USER.md, MEMORY.md, or anything under skills/ — use write_file for those files and manage_skill for skills. " +
				"Reading protected paths (cat, ls, grep) is allowed. Every invocation is logged to .bash-audit.log.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The shell command to run."},
					"timeout_seconds": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds (default 30, max 300).",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}
func (t *bashTool) Run(ctx context.Context, args string) (string, error) {
	var a struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", err
	}
	if a.Command == "" {
		return "", fmt.Errorf("command is required")
	}
	if err := safeguard(a.Command); err != nil {
		t.audit(a.Command, "blocked", err.Error())
		return "", err
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 300 {
		timeout = 300
	}
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, "bash", "-c", a.Command)
	cmd.Dir = t.dataDir
	cmd.Env = t.redactor.ChildEnv()
	out, err := cmd.CombinedOutput()
	result := string(out)
	status := "ok"
	if err != nil {
		status = fmt.Sprintf("err: %v", err)
	}
	t.audit(a.Command, status, "")
	if err != nil {
		return fmt.Sprintf("exit error: %v\n%s", err, result), nil
	}
	if result == "" {
		return "(no output)", nil
	}
	return result, nil
}

// audit appends a one-line record to <dataDir>/.bash-audit.log. Failures
// are best-effort — never block the tool call on logging.
func (t *bashTool) audit(command, status, blockedReason string) {
	path := filepath.Join(t.dataDir, ".bash-audit.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	// Replace newlines so each record stays one line.
	flat := strings.ReplaceAll(command, "\n", "\\n")
	stamp := time.Now().UTC().Format(time.RFC3339)
	if blockedReason != "" {
		_, _ = fmt.Fprintf(f, "%s\t%s\t%s\t%s\n", stamp, status, flat, blockedReason)
	} else {
		_, _ = fmt.Fprintf(f, "%s\t%s\t%s\n", stamp, status, flat)
	}
}

// ---- search_wiki ----

type searchWiki struct{ dataDir string }

func (t *searchWiki) Name() string { return "search_wiki" }
func (t *searchWiki) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name:        "search_wiki",
			Description: "Search the agent's wiki knowledge base. Returns the top 3 most relevant chunks.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Search query."},
				},
				"required": []string{"query"},
			},
		},
	}
}
func (t *searchWiki) Run(_ context.Context, args string) (string, error) {
	var a struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", err
	}
	if a.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	results, err := wiki.Search(filepath.Join(t.dataDir, "wiki"), a.Query, 3)
	if err != nil {
		return "", err
	}
	return wiki.FormatResults(results), nil
}
