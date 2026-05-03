package skills

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

//go:embed builtins/*.md
var BuiltinFS embed.FS

// Entry describes a discovered skill file.
type Entry struct {
	Name        string
	Description string
	File        string
}

// Load reads agentskills.io-style markdown files from dir. It extracts the
// optional frontmatter name and description fields; if absent, it falls
// back to the filename and the first non-empty line.
func Load(dir string) ([]Entry, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		if e.IsDir() {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			rel := filepath.Join(e.Name(), "SKILL.md")
			data, err := os.ReadFile(filepath.Join(dir, rel))
			if err != nil {
				continue
			}
			name, desc := ParseFrontmatter(string(data))
			if name == "" {
				name = e.Name()
			}
			if desc == "" {
				desc = firstNonEmptyLine(string(data))
			}
			out = append(out, Entry{Name: name, Description: desc, File: rel})
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		name, desc := ParseFrontmatter(string(data))
		if name == "" {
			name = strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		}
		if desc == "" {
			desc = firstNonEmptyLine(string(data))
		}
		out = append(out, Entry{Name: name, Description: desc, File: e.Name()})
	}
	return out, nil
}

// ParseFrontmatter extracts name and description from YAML frontmatter.
// Returns empty strings if the content has no frontmatter block.
func ParseFrontmatter(s string) (name, desc string) {
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

// SeedBuiltins writes embedded built-in skills into skills/ if they are
// missing. It does NOT overwrite existing files, so the user (or the
// agent itself) can edit a skill without it being clobbered on restart.
func SeedBuiltins(skillsDir string) error {
	entries, err := BuiltinFS.ReadDir("builtins")
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		dst := filepath.Join(skillsDir, e.Name())
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := BuiltinFS.ReadFile("builtins/" + e.Name())
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

// FormatSkillList formats a slice of entries as a markdown bullet list
// suitable for embedding in a system prompt.
func FormatSkillList(entries []Entry) string {
	var b strings.Builder
	b.WriteString("# Skills available\n")
	b.WriteString("You have access to the following skills. Read the corresponding file when one is relevant.\n\n")
	for _, s := range entries {
		fmt.Fprintf(&b, "- %s — %s (file: skills/%s)\n", s.Name, s.Description, s.File)
	}
	b.WriteString("\n")
	return b.String()
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
