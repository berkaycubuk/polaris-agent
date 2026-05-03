// Package captioner produces text descriptions of images using a separate
// vision-capable LLM. The captioner is intentionally decoupled from the
// agent's main LLM so a small, cheap vision model (e.g. gemini-2.5-flash-lite)
// can run image-to-text while a different model handles reasoning.
package captioner

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

const defaultPrompt = `Describe this image so a downstream assistant can act on it without seeing the picture itself.

Cover, in this order:
1. What the image is overall (a photo of a receipt, a screenshot of a chat, a whiteboard, a meal, etc.).
2. Any visible text — transcribe it verbatim, preserving line breaks and structure (lists, tables, prices).
3. Key objects, people, places, and visible state (counts, colors, conditions).
4. Anything noteworthy a user would likely ask about.

Be thorough but compact. Plain prose plus quoted text where appropriate. No preamble, no markdown headings.`

type Captioner struct {
	llm    *llm.Client
	prompt string
}

func New(client *llm.Client) *Captioner {
	return &Captioner{llm: client, prompt: defaultPrompt}
}

// Caption returns a textual description of the image. Caller's text (if any)
// is appended to the prompt so the captioner knows what the user is asking,
// which often produces a more relevant description.
func (c *Captioner) Caption(ctx context.Context, data []byte, mime, userText string) (string, error) {
	if mime == "" {
		mime = "image/jpeg"
	}
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	prompt := c.prompt
	if t := strings.TrimSpace(userText); t != "" {
		prompt += "\n\nThe user said: " + t
	}
	msgs := []llm.Message{
		{
			Role: llm.RoleUser,
			ContentParts: []llm.Part{
				{Type: "text", Text: prompt},
				{Type: "image_url", ImageURL: &llm.ImageURL{URL: dataURL}},
			},
		},
	}
	resp, err := c.llm.Chat(ctx, msgs, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}
