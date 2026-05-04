package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeguard_BlocksCatastrophic(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"rm-rf-root", "rm -rf /"},
		{"rm-rf-root-glob", "rm -rf /*"},
		{"rm-rf-cwd", "rm -rf ."},
		{"rm-rf-cwd-slash", "rm -rf ./"},
		{"rm-rf-cwd-glob", "rm -rf ./*"},
		{"rm-rf-parent", "rm -rf .."},
		{"rm-rf-home-tilde", "rm -rf ~"},
		{"rm-rf-home-env", "rm -rf $HOME"},
		{"rm-rf-glob", "rm -rf *"},
		{"rm-fr-order", "rm -fr ."},
		{"rm-r-then-f", "rm -r -f /"},
		{"rm-recursive-force", "rm --recursive --force /"},
		{"find-delete", `find . -name "*.md" -delete`},
		{"find-exec-rm", `find . -type f -exec rm {} \;`},
		{"dd-of-dev", "dd if=/dev/zero of=/dev/sda"},
		{"mkfs", "mkfs.ext4 /dev/sda1"},
		{"redirect-dev", "echo hi > /dev/sda"},
		{"fork-bomb", ":(){ :|:& };:"},
		{"chmod-recursive-root", "chmod -R 000 /"},
		{"with-leading-text", "echo cleaning && rm -rf ."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := safeguard(tc.cmd); err == nil {
				t.Errorf("expected block for %q", tc.cmd)
			}
		})
	}
}

func TestSafeguard_AllowsBenign(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"ls", "ls -la skills/"},
		{"cat-soul", "cat SOUL.md"},
		{"grep-skills", "grep -r 'foo' skills/"},
		{"narrow-rm", "rm wiki/draft.md"},
		{"narrow-rm-rf", "rm -rf wiki/old-stuff"},
		{"narrow-rm-rf-named", "rm -rf data/cache"},
		{"git-status", "git -C /app/data status"},
		{"echo", `echo "hello world"`},
		{"uv-run", "uv run scripts/foo.py"},
		{"awk-pipeline", "ls | awk '{print $1}'"},
		{"rm-tmp-glob", "rm /tmp/foo/*"},
		{"find-print", "find . -name '*.md' -print"},
		{"dd-file", "dd if=input.bin of=output.bin bs=1M"},
		{"chmod-file", "chmod 644 wiki/foo.md"},
		{"rm-rf-dotdir", "rm -rf .cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := safeguard(tc.cmd); err != nil {
				t.Errorf("unexpected block for %q: %v", tc.cmd, err)
			}
		})
	}
}

func TestSafeguard_BlocksProtectedFiles(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"redirect-soul", "echo updated > SOUL.md"},
		{"append-user", `printf "fact\n" >> USER.md`},
		{"rm-soul", "rm SOUL.md"},
		{"mv-onto-user", "mv tmp.md USER.md"},
		{"cp-onto-soul", "cp draft.md SOUL.md"},
		{"sed-i-soul", `sed -i 's/foo/bar/' SOUL.md`},
		{"tee-user", `echo x | tee USER.md`},
		{"redirect-relative-soul", "echo x > ./SOUL.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := safeguard(tc.cmd); err == nil {
				t.Errorf("expected block for %q", tc.cmd)
			}
		})
	}
}

func TestSafeguard_BlocksProtectedSkillsDir(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{"rm-skills-dir", "rm -rf skills/old-thing"},
		{"redirect-into-skills", "echo body > skills/new.md"},
		{"mv-into-skills", "mv tmp.md skills/foo.md"},
		{"rm-archive", "rm -rf skills/.archive"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := safeguard(tc.cmd); err == nil {
				t.Errorf("expected block for %q", tc.cmd)
			}
		})
	}
}

func TestSafeguard_AllowsReadingProtected(t *testing.T) {
	cases := []string{
		"cat SOUL.md",
		"grep foo USER.md",
		"ls skills/",
		"ls -la skills/",
		"head SOUL.md",
		"wc -l skills/*.md",
		`grep -r "shopping" skills/`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			if err := safeguard(cmd); err != nil {
				t.Errorf("unexpected block for %q: %v", cmd, err)
			}
		})
	}
}

func TestBashTool_BlockedAuditLogged(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(dir)

	args, _ := json.Marshal(map[string]any{"command": "rm -rf /"})
	_, err := r.Run(context.Background(), "bash", string(args))
	if err == nil {
		t.Fatal("expected block")
	}

	logBytes, readErr := os.ReadFile(filepath.Join(dir, ".bash-audit.log"))
	if readErr != nil {
		t.Fatalf("audit log not written: %v", readErr)
	}
	logs := string(logBytes)
	if !strings.Contains(logs, "blocked") || !strings.Contains(logs, "rm -rf /") {
		t.Errorf("audit log missing expected entries:\n%s", logs)
	}
}

func TestBashTool_OkAuditLogged(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(dir)

	args, _ := json.Marshal(map[string]any{"command": "echo hi"})
	if _, err := r.Run(context.Background(), "bash", string(args)); err != nil {
		t.Fatal(err)
	}

	logBytes, readErr := os.ReadFile(filepath.Join(dir, ".bash-audit.log"))
	if readErr != nil {
		t.Fatalf("audit log not written: %v", readErr)
	}
	logs := string(logBytes)
	if !strings.Contains(logs, "echo hi") {
		t.Errorf("audit log missing entry:\n%s", logs)
	}
}
