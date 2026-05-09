package agent

import (
	"strings"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

// pruneHistory drops oldest exchanges from h until total content is under
// capChars, preserving (a) the leading system message and (b) tool-call /
// tool-result pairing inside each exchange.
//
// An "exchange" starts at a user message and runs until the next user
// message. Dropping at exchange boundaries is critical: a tool-result
// message stranded without its assistant tool_call (or vice versa) makes
// the upstream API reject the whole request.
//
// Returns h unchanged when capChars <= 0 (pruning disabled), when h is
// already under cap, or when only one exchange remains (further pruning
// would either kill the system message or break tool pairing).
func pruneHistory(h []llm.Message, capChars int) []llm.Message {
	if capChars <= 0 || len(h) <= 1 {
		return h
	}
	if historyChars(h) <= capChars {
		return h
	}

	// Split off a leading system message so we never drop it.
	var sys *llm.Message
	body := h
	if h[0].Role == llm.RoleSystem {
		s := h[0]
		sys = &s
		body = h[1:]
	}

	for historyChars(reattach(sys, body)) > capChars {
		next := nextUserIdx(body, 1)
		if next < 0 {
			// Only one exchange left in the body; can't drop it without
			// orphaning tool messages or losing the active turn.
			break
		}
		body = body[next:]
	}
	return reattach(sys, body)
}

// nextUserIdx returns the index of the next user-role message in h at or
// after start, or -1 if none. The first user message in a body marks the
// start of an exchange — pruning truncates body to start from there.
func nextUserIdx(h []llm.Message, start int) int {
	for i := start; i < len(h); i++ {
		if h[i].Role == llm.RoleUser {
			return i
		}
	}
	return -1
}

func reattach(sys *llm.Message, body []llm.Message) []llm.Message {
	if sys == nil {
		return body
	}
	out := make([]llm.Message, 0, 1+len(body))
	out = append(out, *sys)
	out = append(out, body...)
	return out
}

// historyChars sums the user-visible content size of h. Counts message
// content, multimodal text parts, image URL strings, and tool-call
// arguments. Roles, IDs, and JSON envelope overhead are ignored — they're
// small relative to body content and stable across providers.
func historyChars(h []llm.Message) int {
	n := 0
	for _, m := range h {
		n += len(m.Content)
		for _, p := range m.ContentParts {
			n += len(p.Text)
			if p.ImageURL != nil {
				n += len(p.ImageURL.URL)
			}
		}
		for _, tc := range m.ToolCalls {
			n += len(tc.Function.Name) + len(tc.Function.Arguments)
		}
	}
	return n
}

// looksLikeContextOverflow detects upstream "prompt too long" errors so
// the agent can retry once with a tighter prune. Different providers use
// different wording; the heuristic catches the common phrases without
// over-matching unrelated 4xx errors.
func looksLikeContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	// Phrase-based patterns. Provider examples:
	//   OpenAI:    "maximum context length is ..."
	//   Anthropic: "prompt is too long"
	//   Gemini:    "prompt exceeds max length" / code 1261
	//   OpenRouter passes through provider strings.
	patterns := []string{
		"context length",
		"context window",
		"max length",
		"maximum length",
		"too long",
		"too many tokens",
		"prompt exceeds",
		"1261",
	}
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
