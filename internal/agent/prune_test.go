package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

// makeHistory builds a synthetic conversation: leading system message,
// then `n` exchanges of (user → assistant). Each message is `bodyChars`
// long so total chars are easy to reason about.
func makeHistory(n int, bodyChars int) []llm.Message {
	body := strings.Repeat("x", bodyChars)
	h := []llm.Message{{Role: llm.RoleSystem, Content: "SYSTEM"}}
	for i := 0; i < n; i++ {
		h = append(h,
			llm.Message{Role: llm.RoleUser, Content: body},
			llm.Message{Role: llm.RoleAssistant, Content: body},
		)
	}
	return h
}

func TestPruneHistory_NoOpUnderCap(t *testing.T) {
	h := makeHistory(3, 100)
	out := pruneHistory(h, 100000)
	if len(out) != len(h) {
		t.Fatalf("expected no prune, got %d -> %d", len(h), len(out))
	}
}

func TestPruneHistory_DropsOldestExchanges(t *testing.T) {
	h := makeHistory(5, 1000) // ~10000 chars + sys
	out := pruneHistory(h, 3500)
	if out[0].Role != llm.RoleSystem || out[0].Content != "SYSTEM" {
		t.Fatalf("system message not preserved: %+v", out[0])
	}
	// Must end on the most recent exchange's assistant message.
	if out[len(out)-1].Role != llm.RoleAssistant {
		t.Fatalf("last message should be assistant, got %s", out[len(out)-1].Role)
	}
	// First non-system message must be a user message (exchange boundary).
	if out[1].Role != llm.RoleUser {
		t.Fatalf("first body message should be user (exchange start), got %s", out[1].Role)
	}
	if historyChars(out) > 3500 {
		t.Fatalf("post-prune chars %d exceed cap 3500", historyChars(out))
	}
}

func TestPruneHistory_PreservesToolCallPairing(t *testing.T) {
	body := strings.Repeat("a", 500)
	// Exchange 1: user, assistant (tool_call), tool, assistant
	// Exchange 2: user, assistant
	h := []llm.Message{
		{Role: llm.RoleSystem, Content: "SYS"},
		{Role: llm.RoleUser, Content: body},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "t1", Function: llm.FunctionCall{Name: "bash", Arguments: body}}}},
		{Role: llm.RoleTool, ToolCallID: "t1", Content: body},
		{Role: llm.RoleAssistant, Content: body},
		{Role: llm.RoleUser, Content: body},
		{Role: llm.RoleAssistant, Content: body},
	}
	// Tight cap forces dropping the first exchange wholesale.
	out := pruneHistory(h, 1500)
	// Should not contain the orphaned tool message any more.
	for _, m := range out {
		if m.Role == llm.RoleTool && m.ToolCallID == "t1" {
			t.Fatal("tool message survived without its assistant tool_call — pairing broken")
		}
	}
	// And no assistant tool_call without a following tool result either.
	for i, m := range out {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			// Next message should be a tool result for one of these IDs.
			if i+1 >= len(out) || out[i+1].Role != llm.RoleTool {
				t.Fatal("assistant tool_call survived without its tool result — pairing broken")
			}
		}
	}
}

func TestPruneHistory_KeepsAtLeastSystemPlusOneExchange(t *testing.T) {
	// Even with a cap below the size of a single exchange, we should not
	// drop the only exchange — that would leave the agent with no current
	// turn to respond to.
	h := makeHistory(1, 5000)
	out := pruneHistory(h, 100)
	if len(out) < 3 {
		t.Fatalf("expected system + last exchange to survive, got %d msgs", len(out))
	}
	if out[0].Role != llm.RoleSystem {
		t.Fatalf("system not preserved")
	}
}

func TestPruneHistory_DisabledWhenCapZero(t *testing.T) {
	h := makeHistory(10, 1000)
	out := pruneHistory(h, 0)
	if len(out) != len(h) {
		t.Fatalf("cap=0 should disable pruning, got %d -> %d", len(h), len(out))
	}
}

func TestLooksLikeContextOverflow(t *testing.T) {
	cases := map[string]bool{
		"prompt exceeds max length":                            true,
		"code: 1261 message: prompt too long":                  true,
		"This model's maximum context length is 128000 tokens": true,
		"too many tokens":                                      true,
		"context window exceeded":                              true,
		"unauthorized":                                         false,
		"rate limit reached":                                   false,
		"":                                                     false,
	}
	for msg, want := range cases {
		var err error
		if msg != "" {
			err = errors.New(msg)
		}
		if got := looksLikeContextOverflow(err); got != want {
			t.Errorf("looksLikeContextOverflow(%q) = %v, want %v", msg, got, want)
		}
	}
}
