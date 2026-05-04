package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManageMemory_AddAndView(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	out, err := r.Run(context.Background(), "manage_memory",
		`{"action":"add","scope":"user","content":"name: Berkay"}`)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "USER.md") || !strings.Contains(out, "/1375") {
		t.Fatalf("add should report file and cap: %q", out)
	}

	if _, err := r.Run(context.Background(), "manage_memory",
		`{"action":"add","scope":"user","content":"role: founder"}`); err != nil {
		t.Fatalf("second add: %v", err)
	}

	on, err := os.ReadFile(filepath.Join(dir, "USER.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != "name: Berkay\nrole: founder" {
		t.Fatalf("add should append with newline separator, got %q", on)
	}

	view, err := r.Run(context.Background(), "manage_memory",
		`{"action":"view","scope":"user"}`)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if !strings.Contains(view, "name: Berkay") || !strings.Contains(view, "role: founder") {
		t.Fatalf("view should show both entries: %q", view)
	}
}

func TestManageMemory_AddOverCap(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	// Pre-fill close to cap. memory cap is 2200.
	prefill := strings.Repeat("a", 2100)
	if _, err := r.Run(context.Background(), "manage_memory",
		`{"action":"add","scope":"memory","content":"`+prefill+`"}`); err != nil {
		t.Fatalf("prefill: %v", err)
	}

	// Now an add of 200 chars overflows (2100 + 1 separator + 200 = 2301).
	overflow := strings.Repeat("b", 200)
	_, err := r.Run(context.Background(), "manage_memory",
		`{"action":"add","scope":"memory","content":"`+overflow+`"}`)
	if err == nil {
		t.Fatal("expected over-cap error")
	}
	if !strings.Contains(err.Error(), "rewrite") || !strings.Contains(err.Error(), "wiki") {
		t.Fatalf("error should suggest rewrite or wiki overflow: %v", err)
	}

	// File must be unchanged (still the prefill).
	on, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != prefill {
		t.Fatal("MEMORY.md should be unchanged after rejected add")
	}
}

func TestManageMemory_Rewrite(t *testing.T) {
	dir := t.TempDir()
	r := NewRegistry(dir)

	// Seed with two entries via add.
	for _, c := range []string{"old1", "old2"} {
		if _, err := r.Run(context.Background(), "manage_memory",
			`{"action":"add","scope":"memory","content":"`+c+`"}`); err != nil {
			t.Fatalf("seed add: %v", err)
		}
	}
	// Rewrite collapses them into a summary.
	if _, err := r.Run(context.Background(), "manage_memory",
		`{"action":"rewrite","scope":"memory","content":"summary of olds"}`); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	on, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(on) != "summary of olds" {
		t.Fatalf("rewrite should replace contents, got %q", on)
	}
}

func TestManageMemory_RewriteOverCap(t *testing.T) {
	r := NewRegistry(t.TempDir())
	big := strings.Repeat("y", 1376) // user cap is 1375
	_, err := r.Run(context.Background(), "manage_memory",
		`{"action":"rewrite","scope":"user","content":"`+big+`"}`)
	if err == nil || !strings.Contains(err.Error(), "1375") {
		t.Fatalf("expected cap error, got %v", err)
	}
}

func TestManageMemory_BadScope(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "manage_memory",
		`{"action":"add","scope":"wiki","content":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("expected scope error, got %v", err)
	}
}

func TestManageMemory_BadAction(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "manage_memory",
		`{"action":"set","scope":"user","content":"x"}`)
	if err == nil || !strings.Contains(err.Error(), "action") {
		t.Fatalf("expected action error, got %v", err)
	}
}

func TestManageMemory_AddEmpty(t *testing.T) {
	r := NewRegistry(t.TempDir())
	_, err := r.Run(context.Background(), "manage_memory",
		`{"action":"add","scope":"user","content":""}`)
	if err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("expected empty-content error, got %v", err)
	}
}

func TestManageMemory_ViewMissing(t *testing.T) {
	r := NewRegistry(t.TempDir())
	out, err := r.Run(context.Background(), "manage_memory",
		`{"action":"view","scope":"memory"}`)
	if err != nil {
		t.Fatalf("view of missing file should not error: %v", err)
	}
	if !strings.Contains(out, "empty") {
		t.Fatalf("view of missing file should say empty: %q", out)
	}
}
