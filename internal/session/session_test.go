package session

import (
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

func TestStoreGetMissing(t *testing.T) {
	s := NewStore()
	if h := s.Get("nope"); h != nil {
		t.Fatalf("expected nil for missing session, got %v", h)
	}
}

func TestStoreSetAndGet(t *testing.T) {
	s := NewStore()
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	s.Set("s1", msgs)
	got := s.Get("s1")
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "hello" || got[1].Content != "hi" {
		t.Fatalf("unexpected messages: %+v", got)
	}
}

func TestGetReturnsCopy(t *testing.T) {
	s := NewStore()
	s.Set("s1", []llm.Message{{Role: llm.RoleUser, Content: "a"}})
	got := s.Get("s1")
	got[0].Content = "mutated"
	if s.Get("s1")[0].Content != "a" {
		t.Fatal("Get should return a copy, not a reference")
	}
}

func TestDelete(t *testing.T) {
	s := NewStore()
	s.Set("s1", []llm.Message{{Role: llm.RoleUser, Content: "a"}})
	s.Delete("s1")
	if h := s.Get("s1"); h != nil {
		t.Fatalf("expected nil after delete, got %v", h)
	}
}
