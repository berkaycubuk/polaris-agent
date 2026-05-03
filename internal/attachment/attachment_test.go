package attachment

import (
	"context"
	"strings"
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

func TestComposeNoAttachments(t *testing.T) {
	p := NewProcessor(nil, nil)
	msg, err := p.Compose(context.Background(), "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != "hello" || len(msg.ContentParts) != 0 {
		t.Fatalf("unexpected message: %+v", msg)
	}
}

func TestComposeWithoutCaptionerOrR2(t *testing.T) {
	p := NewProcessor(nil, nil)
	imgBytes := []byte{0xFF, 0xD8, 0xFF, 0xD9} // tiny fake JPEG
	msg, err := p.Compose(context.Background(), "what is this?", []Attachment{
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
	if !strings.Contains(msg.Content, "no captioner") {
		t.Fatalf("expected 'no captioner' notice, got: %s", msg.Content)
	}
}

func TestComposeWireFormatIsTextOnly(t *testing.T) {
	p := NewProcessor(nil, nil)
	msg, _ := p.Compose(context.Background(), "x", []Attachment{
		{Data: []byte{0x01, 0x02}, MimeType: "image/png"},
	})
	if msg.Role != llm.RoleUser {
		t.Fatalf("wrong role: %s", msg.Role)
	}
	if msg.Content == "" {
		t.Fatal("expected text content")
	}
}

func TestExtFromMime(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		{"application/octet-stream", ".bin"},
	}
	for _, tc := range tests {
		got := ExtFromMime(tc.mime)
		if got != tc.want {
			t.Errorf("ExtFromMime(%q) = %q, want %q", tc.mime, got, tc.want)
		}
	}
}
