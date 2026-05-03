package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeedBuiltins(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SeedBuiltins(skillsDir); err != nil {
		t.Fatalf("SeedBuiltins: %v", err)
	}

	dst := filepath.Join(skillsDir, "skill-builder.md")
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("expected skill-builder.md to be seeded: %v", err)
	}
	if !strings.Contains(string(data), "name: skill-builder") {
		t.Fatalf("seeded skill missing frontmatter; got: %s", string(data[:200]))
	}

	// Edit the file; second seed must not overwrite it.
	custom := []byte("user-edited content")
	if err := os.WriteFile(dst, custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SeedBuiltins(skillsDir); err != nil {
		t.Fatalf("second SeedBuiltins: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != string(custom) {
		t.Fatalf("user edit was overwritten: %s", string(got))
	}
}

func TestLoadDirectoryLayout(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, "youtube-music"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(skillsDir, "youtube-music", "SKILL.md")
	body := "---\nname: youtube-music\ndescription: manage YouTube Music playlists via ytmusicapi\n---\n\n# YouTube Music\n"
	if err := os.WriteFile(skillFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	// Hidden directories (e.g. .uv-cache) must be ignored.
	if err := os.MkdirAll(filepath.Join(skillsDir, ".uv-cache"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := Load(skillsDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 skill, got %d (%+v)", len(entries), entries)
	}
	got := entries[0]
	if got.Name != "youtube-music" || got.File != "youtube-music/SKILL.md" {
		t.Fatalf("unexpected entry: %+v", got)
	}
	if !strings.Contains(got.Description, "ytmusicapi") {
		t.Fatalf("description not parsed: %q", got.Description)
	}
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		input     string
		wantName  string
		wantDesc  string
	}{
		{
			input:     "---\nname: foo\ndescription: bar\n---\nbody",
			wantName:  "foo",
			wantDesc:  "bar",
		},
		{
			input:    "no frontmatter here",
			wantName: "",
			wantDesc: "",
		},
		{
			input:    "---\nname: quoted-name\n---\nbody",
			wantName: "quoted-name",
			wantDesc: "",
		},
	}

	for _, tc := range tests {
		name, desc := ParseFrontmatter(tc.input)
		if name != tc.wantName {
			t.Errorf("ParseFrontmatter(%q) name = %q, want %q", tc.input[:min(len(tc.input), 40)], name, tc.wantName)
		}
		if desc != tc.wantDesc {
			t.Errorf("ParseFrontmatter(%q) desc = %q, want %q", tc.input[:min(len(tc.input), 40)], desc, tc.wantDesc)
		}
	}
}
