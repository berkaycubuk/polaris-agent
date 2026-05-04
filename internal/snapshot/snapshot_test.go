package snapshot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func gitLog(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--pretty=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return string(out)
}

func TestNew_DisabledReturnsNil(t *testing.T) {
	if s := New(t.TempDir(), false); s != nil {
		t.Errorf("expected nil for disabled, got %v", s)
	}
}

func TestNew_DisabledSnapshotterIsNoOp(t *testing.T) {
	var s *Snapshotter // mimics the disabled case
	if s.Enabled() {
		t.Error("nil receiver should report disabled")
	}
	if err := s.Commit("any"); err != nil {
		t.Errorf("nil Commit should be no-op, got %v", err)
	}
}

func TestNew_InitsRepoAndGitignore(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	s := New(dir, true)
	if s == nil {
		t.Fatal("expected enabled snapshotter")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Errorf(".git missing: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, want := range []string{"secrets/", ".bash-audit.log", ".uv-cache/"} {
		if !strings.Contains(string(gi), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, gi)
		}
	}
	if strings.Contains(string(gi), ".archive") {
		t.Error(".archive should NOT be ignored — it's the recovery safety net")
	}
}

func TestCommit_StoresFileChanges(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	s := New(dir, true)
	if s == nil {
		t.Fatal("snapshot disabled unexpectedly")
	}
	if err := os.WriteFile(filepath.Join(dir, "SOUL.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("session-a"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	log := gitLog(t, dir)
	if !strings.Contains(log, "session=session-a") {
		t.Errorf("commit message missing session id:\n%s", log)
	}
}

func TestCommit_SkipsEmpty(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	s := New(dir, true)
	beforeLog := gitLog(t, dir)
	if err := s.Commit("session-a"); err != nil {
		t.Fatal(err)
	}
	afterLog := gitLog(t, dir)
	if beforeLog != afterLog {
		t.Errorf("empty commit was not skipped:\nbefore=%s\nafter=%s", beforeLog, afterLog)
	}
}

func TestCommit_IgnoresGitignoredPaths(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	s := New(dir, true)
	if err := os.WriteFile(filepath.Join(dir, ".bash-audit.log"), []byte("noise"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets/api.key"), []byte("supersecret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("session-a"); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", dir, "ls-files").Output()
	files := string(out)
	if strings.Contains(files, ".bash-audit.log") {
		t.Errorf(".bash-audit.log should be gitignored, got tracked:\n%s", files)
	}
	if strings.Contains(files, "secrets/") || strings.Contains(files, "api.key") {
		t.Errorf("secrets/ should be gitignored, got tracked:\n%s", files)
	}
}

func TestCommit_TracksArchiveDir(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	s := New(dir, true)
	if err := os.MkdirAll(filepath.Join(dir, "skills/.archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills/.archive/old.md"), []byte("retired"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("session-a"); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", dir, "ls-files").Output()
	files := string(out)
	if !strings.Contains(files, "skills/.archive/old.md") {
		t.Errorf("expected archived skill to be tracked:\n%s", files)
	}
}

func TestNew_StrayChangesRecovered(t *testing.T) {
	gitAvailable(t)
	dir := t.TempDir()
	// First lifecycle: init, then write a file but don't commit (simulating a crash).
	s := New(dir, true)
	if s == nil {
		t.Fatal("snapshot init failed")
	}
	if err := os.WriteFile(filepath.Join(dir, "wiki.md"), []byte("interrupted"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Verify file is uncommitted.
	out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if !strings.Contains(string(out), "wiki.md") {
		t.Fatalf("expected dirty state, got: %s", out)
	}

	// Second lifecycle: re-init. Should commit the stray file.
	s = New(dir, true)
	if s == nil {
		t.Fatal("re-init failed")
	}
	out, _ = exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("expected clean state after stray-recovery, got: %s", out)
	}
	log := gitLog(t, dir)
	if !strings.Contains(log, "stray changes recovered") {
		t.Errorf("expected stray-recovery commit, got log:\n%s", log)
	}
}

func TestSanitizeSession(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"normal-session", "normal-session"},
		{"with\nnewline", "withnewline"},
		{"with\ttab\rcr", "withtabcr"},
		{strings.Repeat("a", 100), strings.Repeat("a", 64)},
	}
	for _, tc := range cases {
		if got := sanitizeSession(tc.in); got != tc.want {
			t.Errorf("sanitizeSession(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
