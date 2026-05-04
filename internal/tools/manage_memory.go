package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

// memoryScope describes one of the agent's hot-memory files.
type memoryScope struct {
	file  string
	limit int
}

var memoryScopes = map[string]memoryScope{
	"user":   {file: "USER.md", limit: 1375},
	"memory": {file: "MEMORY.md", limit: 2200},
}

// manageMemory is the only writer for USER.md and MEMORY.md. It enforces
// the per-file char cap and reports current usage so the agent can decide
// whether to summarize or push older notes into the wiki.
type manageMemory struct{ dataDir string }

func (t *manageMemory) Name() string { return "manage_memory" }

func (t *manageMemory) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name: "manage_memory",
			Description: "Manage the agent's hot working memory: USER.md (lasting facts about the user, 1375-char cap) " +
				"and MEMORY.md (the agent's personal cross-turn notes, 2200-char cap). Both files load into every " +
				"system prompt, so they must stay tight. Save proactively — don't wait to be asked. " +
				"Action 'add' appends a new entry without touching existing content (this is the default for new notes — safe, won't lose anything). " +
				"Action 'rewrite' replaces the whole file (use only when consolidating, summarizing, or trimming after an over-cap error). " +
				"Action 'view' returns current contents and char usage. " +
				"When a write would exceed the cap, the tool errors — summarize older entries or push them into wiki/<topic>.md, then retry.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"add", "rewrite", "view"},
						"description": "add: append (preserves existing). rewrite: replace whole file. view: return current contents.",
					},
					"scope": map[string]any{
						"type":        "string",
						"enum":        []string{"user", "memory"},
						"description": "user → USER.md, memory → MEMORY.md.",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "Entry to append (action=add) or full new contents (action=rewrite).",
					},
				},
				"required": []string{"action", "scope"},
			},
		},
	}
}

func (t *manageMemory) Run(_ context.Context, args string) (string, error) {
	var a struct {
		Action  string `json:"action"`
		Scope   string `json:"scope"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", err
	}
	scope, ok := memoryScopes[a.Scope]
	if !ok {
		return "", fmt.Errorf("scope must be \"user\" or \"memory\", got %q", a.Scope)
	}
	path := filepath.Join(t.dataDir, scope.file)

	switch a.Action {
	case "view":
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Sprintf("(%s is empty, 0/%d chars)", scope.file, scope.limit), nil
			}
			return "", err
		}
		return fmt.Sprintf("%s (%d/%d chars)\n---\n%s", scope.file, len(b), scope.limit, string(b)), nil

	case "add":
		if a.Content == "" {
			return "", fmt.Errorf("content is required for action=add")
		}
		existing, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		var combined []byte
		if len(existing) > 0 {
			combined = append(existing, '\n')
			combined = append(combined, a.Content...)
		} else {
			combined = []byte(a.Content)
		}
		if len(combined) > scope.limit {
			return "", fmt.Errorf("%s would exceed %d-char cap (current %d + new %d + separator). Use action=\"rewrite\" to summarize older entries, or push older entries into wiki/<topic>.md and try again", scope.file, scope.limit, len(existing), len(a.Content))
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, combined, 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Appended to %s (%d/%d chars)", scope.file, len(combined), scope.limit), nil

	case "rewrite":
		if len(a.Content) > scope.limit {
			return "", fmt.Errorf("%s exceeds %d-char cap (got %d). Trim further or move older entries to wiki/<topic>.md", scope.file, scope.limit, len(a.Content))
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, []byte(a.Content), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Rewrote %s (%d/%d chars)", scope.file, len(a.Content), scope.limit), nil

	default:
		return "", fmt.Errorf("action must be \"add\", \"rewrite\", or \"view\", got %q", a.Action)
	}
}
