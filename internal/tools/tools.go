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
	return r
}

func (r *Registry) register(t Tool) { r.tools[t.Name()] = t }

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
			Name:        "bash",
			Description: "Execute a bash command inside the agent's container. Runs from the data directory by default.",
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
	if err != nil {
		return fmt.Sprintf("exit error: %v\n%s", err, result), nil
	}
	if result == "" {
		return "(no output)", nil
	}
	return result, nil
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
