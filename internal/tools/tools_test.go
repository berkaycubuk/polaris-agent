package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- resolvePath ---

func TestResolvePath_Relative(t *testing.T) {
	dir := t.TempDir()
	p, err := resolvePath(dir, "foo/bar.txt")
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(dir, "foo", "bar.txt")
	if p != expected {
		t.Fatalf("got %q, want %q", p, expected)
	}
}

func TestResolvePath_Empty(t *testing.T) {
	_, err := resolvePath("/data", "")
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected path-is-required error, got %v", err)
	}
}

func TestResolvePath_EscapeAttempt(t *testing.T) {
	dir := t.TempDir()
	_, err := resolvePath(dir, "../../etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "escapes data dir") {
		t.Fatalf("expected escape error, got %v", err)
	}
}

func TestResolvePath_AbsoluteInside(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "sub", "file.txt")
	p, err := resolvePath(dir, abs)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Clean(abs) {
		t.Fatalf("got %q, want %q", p, filepath.Clean(abs))
	}
}

func TestResolvePath_AbsoluteOutside(t *testing.T) {
	dir := t.TempDir()
	_, err := resolvePath(dir, "/tmp/evil")
	if err == nil || !strings.Contains(err.Error(), "escapes data dir") {
		t.Fatalf("expected escape error, got %v", err)
	}
}

// --- Registry ---

func TestRegistrySpecs(t *testing.T) {
	r := NewRegistry(t.TempDir())
	specs := r.Specs()
	names := map[string]bool{}
	for _, s := range specs {
		names[s.Function.Name] = true
	}
	for _, want := range []string{"read_file", "write_file", "bash", "search_wiki"} {
		if !names[want] {
			t.Errorf("missing tool spec: %s", want)
		}
	}
}

func TestRegistryUnknownTool(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "nonexistent", "{}")
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("expected unknown tool error, got %v", err)
	}
}

// --- readFile tool ---

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	content := "hello world\nline 2"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(dir)
	out, err := r.Run(context.Background(), "read_file", `{"path":"test.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != content {
		t.Fatalf("got %q, want %q", out, content)
	}
}

func TestReadFile_NotFound(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "read_file", `{"path":"missing.txt"}`)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFile_Escape(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "read_file", `{"path":"../../etc/passwd"}`)
	if err == nil {
		t.Fatal("expected path escape error")
	}
}

func TestReadFile_InvalidJSON(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "read_file", `not json`)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
}

// --- writeFile tool ---

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	out, err := r.Run(context.Background(), "write_file", `{"path":"sub/dir/file.txt","content":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5 bytes") {
		t.Fatalf("unexpected output: %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("file content = %q, want %q", string(data), "hello")
	}
}

func TestWriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	r.Run(context.Background(), "write_file", `{"path":"f.txt","content":"old"}`)
	r.Run(context.Background(), "write_file", `{"path":"f.txt","content":"new"}`)
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	if string(data) != "new" {
		t.Fatalf("expected overwrite, got %q", string(data))
	}
}

func TestWriteFile_Escape(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "write_file", `{"path":"/tmp/evil.txt","content":"nope"}`)
	if err == nil {
		t.Fatal("expected path escape error")
	}
}

// --- bash tool ---

func TestBash_Echo(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	out, err := r.Run(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected 'hello' in output, got %q", out)
	}
}

func TestBash_WorkingDir(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)
	out, err := r.Run(context.Background(), "bash", `{"command":"pwd"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, dir) {
		t.Fatalf("expected pwd to be %q, got %q", dir, out)
	}
}

func TestBash_EmptyCommand(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "bash", `{"command":""}`)
	if err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("expected command-required error, got %v", err)
	}
}

func TestBash_TimeoutCapped(t *testing.T) {
	r := NewRegistry(t.TempDir())
	// 999s should be capped to 300 internally; the command just echoes
	// so we're just verifying it doesn't error.
	out, err := r.Run(context.Background(), "bash", `{"command":"echo ok","timeout_seconds":999}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ok") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestBash_ExitError(t *testing.T) {
	r := NewRegistry(t.TempDir())
	out, err := r.Run(context.Background(), "bash", `{"command":"exit 1"}`)
	if err != nil {
		// bash tool returns output (with exit error) rather than Go error
		t.Fatalf("bash should capture exit error in output: %v", err)
	}
	if !strings.Contains(out, "exit error") {
		t.Fatalf("expected exit error in output, got %q", out)
	}
}

func TestBash_RedactsSecrets(t *testing.T) {
	t.Setenv("SKILL_SECRET_VAL", "super-secret-value-123456789012345")
	dir := t.TempDir()
	r := NewRegistry(dir)
	out, err := r.Run(context.Background(), "bash", `{"command":"echo super-secret-value-123456789012345"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "super-secret-value-123456789012345") {
		t.Fatalf("secret leaked into output: %q", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in output: %q", out)
	}
}
