package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageMarshalText(t *testing.T) {
	m := Message{Role: RoleUser, Content: "hi"}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `"content":"hi"`) || !strings.Contains(got, `"role":"user"`) {
		t.Fatalf("unexpected wire: %s", got)
	}
}

func TestMessageMarshalMultipart(t *testing.T) {
	m := Message{
		Role: RoleUser,
		ContentParts: []Part{
			{Type: "text", Text: "what is this?"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/jpeg;base64,AAA"}},
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, `"content":[`) {
		t.Fatalf("expected array content, got: %s", got)
	}
	if !strings.Contains(got, `"image_url"`) || !strings.Contains(got, `"data:image/jpeg;base64,AAA"`) {
		t.Fatalf("missing image_url block: %s", got)
	}
}

func TestMessageUnmarshalString(t *testing.T) {
	raw := `{"role":"assistant","content":"hello"}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if m.Content != "hello" || m.Role != RoleAssistant {
		t.Fatalf("got %+v", m)
	}
}

func TestMessageUnmarshalToolCallNoContent(t *testing.T) {
	raw := `{"role":"assistant","tool_calls":[{"id":"1","type":"function","function":{"name":"x","arguments":"{}"}}]}`
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.ToolCalls) != 1 || m.ToolCalls[0].Function.Name != "x" {
		t.Fatalf("got %+v", m)
	}
}
