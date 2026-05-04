package agent

import (
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

func TestNewAgentDefaults(t *testing.T) {
	a := New(nil, nil, t.TempDir(), Options{})
	if a.maxToolIterations != 30 {
		t.Fatalf("expected default 30 iterations, got %d", a.maxToolIterations)
	}
}

func TestResetNonexistent(t *testing.T) {
	a := New(nil, nil, t.TempDir(), Options{})
	a.Reset("nope") // should not panic
}

func TestObserveSkipsEmptySession(t *testing.T) {
	a := New(nil, nil, t.TempDir(), Options{})
	a.Observe("", "ignored")
	a.Observe("nope", "")
	a.Observe("nope", "no anchor yet") // session has no history → drop
	if got := a.sessions.Get("nope"); got != nil {
		t.Fatalf("expected no session created, got %v", got)
	}
}

func TestObserveAppendsToExistingSession(t *testing.T) {
	a := New(nil, nil, t.TempDir(), Options{})
	a.sessions.Set("s", []llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	a.Observe("s", "[I reacted ❤️ to your earlier message]")
	got := a.sessions.Get("s")
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[1].Role != llm.RoleUser || got[1].Content == "" {
		t.Fatalf("unexpected appended message: %+v", got[1])
	}
}
