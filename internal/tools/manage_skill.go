package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/skills"
)

// manageSkill is a structured tool for creating, editing, and archiving
// skill files. It writes valid frontmatter automatically and never deletes:
// archive moves the skill into skills/.archive/ with a timestamp suffix so
// nothing the agent learned is permanently lost.
type manageSkill struct{ dataDir string }

func (t *manageSkill) Name() string { return "manage_skill" }

func (t *manageSkill) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name: "manage_skill",
			Description: "Create, edit, or archive a skill in skills/. Use this instead of write_file when " +
				"working on skills — it generates valid frontmatter and protects you from accidental loss " +
				"(archive moves a skill aside, never deletes). For the authoring guide, read " +
				"skills/skill-builder.md.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"create", "edit", "archive"},
						"description": "create: write a new skill. edit: replace an existing skill's body (and optionally its description). archive: move a skill to skills/.archive/ — reversible.",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "Skill path relative to skills/. Flat skill: '<name>.md'. Directory skill: '<name>/SKILL.md'. Name must be kebab-case.",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Required for create; optional for edit (omit to keep the existing description). One sentence describing when to use this skill — specific about triggers.",
					},
					"body": map[string]any{
						"type":        "string",
						"description": "The skill's markdown body, WITHOUT frontmatter. The tool prepends frontmatter from path + description. Required for create and edit.",
					},
				},
				"required": []string{"action", "path"},
			},
		},
	}
}

var kebabRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func (t *manageSkill) Run(_ context.Context, args string) (string, error) {
	var a struct {
		Action      string `json:"action"`
		Path        string `json:"path"`
		Description string `json:"description"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", fmt.Errorf("parse args: %w", err)
	}

	skillPath, name, err := validateSkillPath(a.Path)
	if err != nil {
		return "", err
	}
	abs, err := resolvePath(t.dataDir, filepath.Join("skills", skillPath))
	if err != nil {
		return "", err
	}

	switch a.Action {
	case "create":
		return t.create(abs, name, a.Description, a.Body, skillPath)
	case "edit":
		return t.edit(abs, name, a.Description, a.Body, skillPath)
	case "archive":
		return t.archive(abs, skillPath)
	default:
		return "", fmt.Errorf("unknown action %q (valid: create, edit, archive)", a.Action)
	}
}

func (t *manageSkill) create(abs, name, description, body, rel string) (string, error) {
	if description == "" {
		return "", fmt.Errorf("description is required for create")
	}
	if body == "" {
		return "", fmt.Errorf("body is required for create")
	}
	if _, err := os.Stat(abs); err == nil {
		return "", fmt.Errorf("skill already exists at skills/%s — use action='edit' or pick a different name", rel)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	content := renderSkill(name, description, body)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created skill at skills/%s (%d bytes).", rel, len(content)), nil
}

func (t *manageSkill) edit(abs, name, description, body, rel string) (string, error) {
	if body == "" {
		return "", fmt.Errorf("body is required for edit")
	}
	existing, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no skill at skills/%s — use action='create' to add it", rel)
		}
		return "", err
	}
	if description == "" {
		// Preserve the existing description.
		_, oldDesc := skills.ParseFrontmatter(string(existing))
		description = oldDesc
	}
	if description == "" {
		return "", fmt.Errorf("could not parse a description from the existing skill at skills/%s — pass one explicitly", rel)
	}
	content := renderSkill(name, description, body)
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited skill at skills/%s (%d bytes).", rel, len(content)), nil
}

func (t *manageSkill) archive(abs, rel string) (string, error) {
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no skill at skills/%s", rel)
		}
		return "", err
	}
	archiveRoot := filepath.Join(t.dataDir, "skills", ".archive")
	if err := os.MkdirAll(archiveRoot, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	// Encode the original path so collisions don't happen when two skills
	// have the same basename in different directories.
	flat := strings.ReplaceAll(rel, string(os.PathSeparator), "__")
	dst := filepath.Join(archiveRoot, fmt.Sprintf("%s.%s", flat, stamp))
	if err := os.Rename(abs, dst); err != nil {
		return "", err
	}
	return fmt.Sprintf("Archived skills/%s to skills/.archive/%s", rel, filepath.Base(dst)), nil
}

// validateSkillPath checks the path is within skills/, well-formed, and
// returns the cleaned path and the derived skill name (used for frontmatter).
func validateSkillPath(p string) (cleaned, name string, err error) {
	if p == "" {
		return "", "", fmt.Errorf("path is required")
	}
	cleaned = filepath.Clean(p)
	if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
		return "", "", fmt.Errorf("path must be relative inside skills/")
	}
	if strings.HasPrefix(cleaned, ".archive") {
		return "", "", fmt.Errorf("cannot manage paths under skills/.archive directly")
	}

	// Two valid shapes: '<name>.md' or '<name>/SKILL.md'.
	switch {
	case strings.HasSuffix(cleaned, "/SKILL.md"):
		name = strings.TrimSuffix(cleaned, "/SKILL.md")
		if strings.Contains(name, "/") {
			return "", "", fmt.Errorf("directory skills must be one level deep: '<name>/SKILL.md'")
		}
	case strings.HasSuffix(cleaned, ".md"):
		base := filepath.Base(cleaned)
		if base != cleaned {
			return "", "", fmt.Errorf("flat skills must live directly under skills/, got %q", cleaned)
		}
		name = strings.TrimSuffix(base, ".md")
	default:
		return "", "", fmt.Errorf("path must end in '.md' or '/SKILL.md'")
	}

	if !kebabRe.MatchString(name) {
		return "", "", fmt.Errorf("skill name %q must be kebab-case (lowercase letters, digits, hyphens)", name)
	}
	return cleaned, name, nil
}

// renderSkill builds a skill file: frontmatter + blank line + body.
func renderSkill(name, description, body string) string {
	body = strings.TrimRight(body, "\n")
	desc := strings.ReplaceAll(description, "\n", " ")
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s\n", name, desc, body)
}
