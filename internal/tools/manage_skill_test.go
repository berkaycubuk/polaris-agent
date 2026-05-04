package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newManageSkill(t *testing.T) (*manageSkill, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &manageSkill{dataDir: dir}, dir
}

func runManage(t *testing.T, ms *manageSkill, args map[string]any) (string, error) {
	t.Helper()
	b, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return ms.Run(context.Background(), string(b))
}

func TestManageSkill_CreateFlat(t *testing.T) {
	ms, dir := newManageSkill(t)
	out, err := runManage(t, ms, map[string]any{
		"action":      "create",
		"path":        "grocery-list.md",
		"description": "Manage the user's shared grocery list when they mention shopping or meal planning.",
		"body":        "# Grocery list\n\nKeep one list at wiki/grocery-list.md.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "skills/grocery-list.md") {
		t.Errorf("unexpected output: %s", out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "skills/grocery-list.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "---\nname: grocery-list\ndescription: Manage the user's shared grocery list") {
		t.Errorf("frontmatter wrong:\n%s", got)
	}
	if !strings.Contains(string(got), "# Grocery list") {
		t.Errorf("body missing:\n%s", got)
	}
}

func TestManageSkill_CreateDirectoryLayout(t *testing.T) {
	ms, dir := newManageSkill(t)
	_, err := runManage(t, ms, map[string]any{
		"action":      "create",
		"path":        "youtube-music/SKILL.md",
		"description": "Control YouTube Music when the user mentions playlists or play queue.",
		"body":        "# YouTube Music\n\nUses ytmusicapi.",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/youtube-music/SKILL.md")); err != nil {
		t.Errorf("expected file: %v", err)
	}
}

func TestManageSkill_CreateRejectsExisting(t *testing.T) {
	ms, _ := newManageSkill(t)
	args := map[string]any{
		"action":      "create",
		"path":        "x.md",
		"description": "desc",
		"body":        "body",
	}
	if _, err := runManage(t, ms, args); err != nil {
		t.Fatal(err)
	}
	_, err := runManage(t, ms, args)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected already-exists error, got %v", err)
	}
}

func TestManageSkill_EditPreservesDescription(t *testing.T) {
	ms, dir := newManageSkill(t)
	if _, err := runManage(t, ms, map[string]any{
		"action":      "create",
		"path":        "foo.md",
		"description": "Original description.",
		"body":        "old body",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runManage(t, ms, map[string]any{
		"action": "edit",
		"path":   "foo.md",
		"body":   "new body",
	}); err != nil {
		t.Fatalf("edit: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "skills/foo.md"))
	if !strings.Contains(string(got), "description: Original description.") {
		t.Errorf("description not preserved:\n%s", got)
	}
	if !strings.Contains(string(got), "new body") {
		t.Errorf("body not updated:\n%s", got)
	}
	if strings.Contains(string(got), "old body") {
		t.Errorf("old body still present:\n%s", got)
	}
}

func TestManageSkill_EditUpdatesDescription(t *testing.T) {
	ms, dir := newManageSkill(t)
	_, _ = runManage(t, ms, map[string]any{
		"action":      "create",
		"path":        "foo.md",
		"description": "old",
		"body":        "body",
	})
	_, err := runManage(t, ms, map[string]any{
		"action":      "edit",
		"path":        "foo.md",
		"description": "new description",
		"body":        "body",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "skills/foo.md"))
	if !strings.Contains(string(got), "description: new description") {
		t.Errorf("description not updated:\n%s", got)
	}
}

func TestManageSkill_EditMissingFails(t *testing.T) {
	ms, _ := newManageSkill(t)
	_, err := runManage(t, ms, map[string]any{
		"action": "edit",
		"path":   "ghost.md",
		"body":   "x",
	})
	if err == nil || !strings.Contains(err.Error(), "no skill") {
		t.Errorf("expected missing-skill error, got %v", err)
	}
}

func TestManageSkill_Archive(t *testing.T) {
	ms, dir := newManageSkill(t)
	_, _ = runManage(t, ms, map[string]any{
		"action":      "create",
		"path":        "old-thing.md",
		"description": "desc",
		"body":        "body",
	})
	out, err := runManage(t, ms, map[string]any{
		"action": "archive",
		"path":   "old-thing.md",
	})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.Contains(out, ".archive") {
		t.Errorf("unexpected output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/old-thing.md")); !os.IsNotExist(err) {
		t.Errorf("original should be gone, stat: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "skills/.archive"))
	if len(entries) != 1 {
		t.Errorf("expected 1 archived file, got %d", len(entries))
	}
}

func TestManageSkill_ArchiveDirectorySkill(t *testing.T) {
	ms, dir := newManageSkill(t)
	_, _ = runManage(t, ms, map[string]any{
		"action":      "create",
		"path":        "scripted/SKILL.md",
		"description": "desc",
		"body":        "body",
	})
	if _, err := runManage(t, ms, map[string]any{
		"action": "archive",
		"path":   "scripted/SKILL.md",
	}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills/scripted/SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("original should be gone")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "skills/.archive"))
	if len(entries) != 1 {
		t.Errorf("expected 1 archived file, got %d", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "scripted__SKILL.md.") {
		t.Errorf("archive name doesn't preserve dir context: %s", entries[0].Name())
	}
}

func TestManageSkill_PathValidation(t *testing.T) {
	ms, _ := newManageSkill(t)
	cases := []struct {
		name string
		path string
	}{
		{"escape", "../etc/passwd.md"},
		{"absolute", "/skills/bad.md"},
		{"missing-md", "noext"},
		{"bad-name", "Not_KebabCase.md"},
		{"too-deep", "a/b/SKILL.md"},
		{"flat-with-dir", "a/b.md"},
		{"archive-dir", ".archive/foo.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runManage(t, ms, map[string]any{
				"action":      "create",
				"path":        tc.path,
				"description": "x",
				"body":        "x",
			})
			if err == nil {
				t.Errorf("expected error for path %q", tc.path)
			}
		})
	}
}

func TestManageSkill_RegisteredInRegistry(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(dir)
	specs := r.Specs()
	found := false
	for _, s := range specs {
		if s.Function.Name == "manage_skill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("manage_skill not registered")
	}
}
