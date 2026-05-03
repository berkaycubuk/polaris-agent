package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

func TestComposeUserMessageNoAttachments(t *testing.T) {
	a := &Agent{}
	msg, err := a.composeUserMessage(context.Background(), "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "hello" || len(msg.ContentParts) != 0 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestComposeUserMessageWithoutCaptionerOrR2(t *testing.T) {
	a := &Agent{}
	imgBytes := []byte{0xFF, 0xD8, 0xFF, 0xD9} // tiny fake JPEG
	msg, err := a.composeUserMessage(context.Background(), "what is this?", []Attachment{
		{Data: imgBytes, MimeType: "image/jpeg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ContentParts) != 0 {
		t.Fatalf("expected text-only message, got parts: %+v", msg.ContentParts)
	}
	if !strings.Contains(msg.Content, "[Image 1") {
		t.Fatalf("expected image marker in content: %s", msg.Content)
	}
	if strings.Contains(msg.Content, "base64") || strings.Contains(msg.Content, "data:image") {
		t.Fatalf("image bytes leaked into history: %s", msg.Content)
	}
	// must mention captioner is disabled
	if !strings.Contains(msg.Content, "no captioner") {
		t.Fatalf("expected 'no captioner' notice, got: %s", msg.Content)
	}
}

// Sanity: the LLM message goes out as plain string content, not multipart.
func TestUserMessageWireFormatIsTextOnly(t *testing.T) {
	a := &Agent{}
	msg, _ := a.composeUserMessage(context.Background(), "x", []Attachment{
		{Data: []byte{0x01, 0x02}, MimeType: "image/png"},
	})
	if msg.Role != llm.RoleUser {
		t.Fatalf("wrong role: %s", msg.Role)
	}
	if msg.Content == "" {
		t.Fatal("expected text content")
	}
}
