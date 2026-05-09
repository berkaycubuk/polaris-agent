package scheduler

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecScriptRunner_RunsAndCapturesStdout(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not available on PATH")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "scripts", "hello.py")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `# /// script
# requires-python = ">=3.10"
# ///
import sys
print("hello from script")
print("debug noise", file=sys.stderr)
`
	if err := os.WriteFile(scriptPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewExecScriptRunner(dir, nil, 30*time.Second)
	out, err := r.RunScript(context.Background(), Job{ID: "j1", Script: "scripts/hello.py"})
	if err != nil {
		t.Fatalf("RunScript: %v", err)
	}
	if out != "hello from script" {
		t.Fatalf("stdout = %q, want %q", out, "hello from script")
	}
}

func TestExecScriptRunner_NonZeroExitFoldsStderr(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not available on PATH")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "scripts", "boom.py")
	_ = os.MkdirAll(filepath.Dir(scriptPath), 0o755)
	body := `import sys; sys.stderr.write("kaboom\n"); sys.exit(2)`
	_ = os.WriteFile(scriptPath, []byte(body), 0o644)

	r := NewExecScriptRunner(dir, nil, 30*time.Second)
	_, err := r.RunScript(context.Background(), Job{ID: "j2", Script: "scripts/boom.py"})
	if err == nil {
		t.Fatal("expected error from non-zero exit")
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestExecScriptRunner_RejectsEscape(t *testing.T) {
	dir := t.TempDir()
	r := NewExecScriptRunner(dir, nil, time.Second)
	_, err := r.RunScript(context.Background(), Job{Script: "../../etc/passwd"})
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("expected escape rejection, got %v", err)
	}
}
