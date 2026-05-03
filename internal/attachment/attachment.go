package attachment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/captioner"
	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/storage"
)

// Attachment is binary content (e.g. an image) attached to a user message.
type Attachment struct {
	Data     []byte
	MimeType string // e.g. "image/jpeg"
}

// Processor turns raw attachments into text-only LLM messages. For each
// image it optionally uploads to R2 and generates a caption via a vision
// model; only the caption text + storage URL enter session history, keeping
// the context window light.
type Processor struct {
	captioner *captioner.Captioner // optional
	r2        *storage.R2          // optional
}

// NewProcessor creates a Processor. Either argument may be nil.
func NewProcessor(cap *captioner.Captioner, r2 *storage.R2) *Processor {
	return &Processor{captioner: cap, r2: r2}
}

// Compose produces a single text-only user LLM message from the given text
// and attachments.
func (p *Processor) Compose(ctx context.Context, text string, atts []Attachment) (llm.Message, error) {
	if len(atts) == 0 {
		return llm.Message{Role: llm.RoleUser, Content: text}, nil
	}

	var b strings.Builder
	if t := strings.TrimSpace(text); t != "" {
		b.WriteString(t)
		b.WriteString("\n\n")
	}

	for i, att := range atts {
		mime := att.MimeType
		if mime == "" {
			mime = "image/jpeg"
		}

		var storedURL string
		if p.r2 != nil {
			key := ImageKey(mime)
			url, err := p.r2.Put(ctx, key, att.Data, mime)
			if err != nil {
				log.Printf("r2 put failed: %v", err)
			} else {
				storedURL = url
			}
		}

		var caption string
		if p.captioner != nil {
			c, err := p.captioner.Caption(ctx, att.Data, mime, text)
			if err != nil {
				log.Printf("caption failed: %v", err)
				caption = "(caption unavailable: " + err.Error() + ")"
			} else {
				caption = c
			}
		} else {
			caption = "(no captioner configured; image content not described)"
		}

		fmt.Fprintf(&b, "[Image %d — %s]\n%s\n", i+1, mime, caption)
		if storedURL != "" {
			fmt.Fprintf(&b, "Stored at: %s\n", storedURL)
		}
		if i < len(atts)-1 {
			b.WriteString("\n")
		}
	}

	return llm.Message{Role: llm.RoleUser, Content: strings.TrimSpace(b.String())}, nil
}

// ImageKey produces a sortable, collision-resistant object key for R2.
// Format: images/YYYY/MM/<unix>-<rand>.<ext>
func ImageKey(mime string) string {
	now := time.Now().UTC()
	var rb [6]byte
	_, _ = rand.Read(rb[:])
	ext := ExtFromMime(mime)
	return fmt.Sprintf("images/%04d/%02d/%d-%s%s",
		now.Year(), int(now.Month()), now.Unix(), hex.EncodeToString(rb[:]), ext)
}

// ExtFromMime returns a file extension for common image MIME types.
func ExtFromMime(m string) string {
	switch m {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".bin"
}
