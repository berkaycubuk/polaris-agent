package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/agent"
)

const apiBase = "https://api.telegram.org/bot"

type Bot struct {
	token string
	agent *agent.Agent
	http  *http.Client
}

func New(token string, a *agent.Agent) *Bot {
	return &Bot{token: token, agent: a, http: &http.Client{Timeout: 70 * time.Second}}
}

type update struct {
	UpdateID int      `json:"update_id"`
	Message  *message `json:"message"`
}

type message struct {
	MessageID int         `json:"message_id"`
	Chat      chat        `json:"chat"`
	From      *user       `json:"from"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Photo     []photoSize `json:"photo"`
	Document  *document   `json:"document"`
}

type photoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int    `json:"file_size"`
}

type document struct {
	FileID   string `json:"file_id"`
	MimeType string `json:"mime_type"`
	FileSize int    `json:"file_size"`
}

type chat struct {
	ID int64 `json:"id"`
}

type user struct {
	ID int64 `json:"id"`
}

type updatesResponse struct {
	OK     bool     `json:"ok"`
	Result []update `json:"result"`
}

type fileResponse struct {
	OK     bool `json:"ok"`
	Result struct {
		FileID   string `json:"file_id"`
		FilePath string `json:"file_path"`
		FileSize int    `json:"file_size"`
	} `json:"result"`
	Description string `json:"description"`
}

func (b *Bot) Run(ctx context.Context) error {
	log.Printf("telegram bot started")
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		ups, err := b.getUpdates(ctx, offset, 60)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("telegram getUpdates: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			if u.Message == nil {
				continue
			}
			if u.Message.Text == "" && len(u.Message.Photo) == 0 && u.Message.Document == nil {
				continue
			}
			b.handleMessage(ctx, u.Message)
		}
	}
}

func (b *Bot) handleMessage(ctx context.Context, m *message) {
	chatID := m.Chat.ID
	session := "telegram:" + strconv.FormatInt(chatID, 10)

	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}

	if text == "/start" {
		_ = b.send(ctx, chatID, "Hi, I'm Polaris. Send me anything — text or photos.")
		return
	}
	if text == "/reset" {
		b.agent.Reset(session)
		_ = b.send(ctx, chatID, "Session cleared.")
		return
	}

	attachments, err := b.collectAttachments(ctx, m)
	if err != nil {
		log.Printf("telegram fetch attachment: %v", err)
		_ = b.send(ctx, chatID, fmt.Sprintf("couldn't fetch attachment: %v", err))
		return
	}

	if text == "" && len(attachments) == 0 {
		return
	}

	chatCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	reply, err := b.agent.Chat(chatCtx, session, text, attachments...)
	if err != nil {
		log.Printf("agent chat: %v", err)
		_ = b.send(ctx, chatID, fmt.Sprintf("error: %v", err))
		return
	}
	if reply == "" {
		reply = "(no reply)"
	}
	if err := b.send(ctx, chatID, reply); err != nil {
		log.Printf("telegram send: %v", err)
	}
}

// collectAttachments downloads any image attachments on the message and
// returns them as agent.Attachments. Photos pick the largest size; documents
// are included only if their MIME type starts with "image/".
func (b *Bot) collectAttachments(ctx context.Context, m *message) ([]agent.Attachment, error) {
	var atts []agent.Attachment
	if len(m.Photo) > 0 {
		largest := m.Photo[0]
		for _, p := range m.Photo[1:] {
			if p.Width*p.Height > largest.Width*largest.Height {
				largest = p
			}
		}
		data, mime, err := b.downloadFile(ctx, largest.FileID)
		if err != nil {
			return nil, err
		}
		if mime == "" {
			mime = "image/jpeg"
		}
		atts = append(atts, agent.Attachment{Data: data, MimeType: mime})
	}
	if m.Document != nil && strings.HasPrefix(m.Document.MimeType, "image/") {
		data, _, err := b.downloadFile(ctx, m.Document.FileID)
		if err != nil {
			return nil, err
		}
		atts = append(atts, agent.Attachment{Data: data, MimeType: m.Document.MimeType})
	}
	return atts, nil
}

// downloadFile resolves a Telegram file_id to its bytes via getFile +
// the file CDN URL. Returns bytes and a best-guess MIME type from the path.
func (b *Bot) downloadFile(ctx context.Context, fileID string) ([]byte, string, error) {
	q := url.Values{}
	q.Set("file_id", fileID)
	u := apiBase + b.token + "/getFile?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("getFile %d: %s", resp.StatusCode, string(body))
	}
	var fr fileResponse
	if err := json.Unmarshal(body, &fr); err != nil {
		return nil, "", err
	}
	if !fr.OK || fr.Result.FilePath == "" {
		return nil, "", fmt.Errorf("getFile not ok: %s", fr.Description)
	}

	dl := "https://api.telegram.org/file/bot" + b.token + "/" + fr.Result.FilePath
	dreq, err := http.NewRequestWithContext(ctx, "GET", dl, nil)
	if err != nil {
		return nil, "", err
	}
	dresp, err := b.http.Do(dreq)
	if err != nil {
		return nil, "", err
	}
	defer dresp.Body.Close()
	if dresp.StatusCode >= 400 {
		raw, _ := io.ReadAll(dresp.Body)
		return nil, "", fmt.Errorf("download %d: %s", dresp.StatusCode, string(raw))
	}
	data, err := io.ReadAll(dresp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, mimeFromPath(fr.Result.FilePath), nil
}

func mimeFromPath(p string) string {
	switch {
	case strings.HasSuffix(p, ".jpg"), strings.HasSuffix(p, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(p, ".png"):
		return "image/png"
	case strings.HasSuffix(p, ".webp"):
		return "image/webp"
	case strings.HasSuffix(p, ".gif"):
		return "image/gif"
	}
	return ""
}

func (b *Bot) getUpdates(ctx context.Context, offset, timeout int) ([]update, error) {
	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	q.Set("timeout", strconv.Itoa(timeout))
	u := apiBase + b.token + "/getUpdates?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("telegram %d: %s", resp.StatusCode, string(body))
	}
	var out updatesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("telegram not ok")
	}
	return out.Result, nil
}

// Telegram caps message length at 4096 chars; chunk longer responses.
func (b *Bot) send(ctx context.Context, chatID int64, text string) error {
	const limit = 4000
	for len(text) > 0 {
		piece := text
		if len(piece) > limit {
			piece = piece[:limit]
		}
		text = text[len(piece):]

		body := map[string]any{"chat_id": chatID, "text": piece}
		data, _ := json.Marshal(body)
		u := apiBase + b.token + "/sendMessage"
		req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(string(data)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := b.http.Do(req)
		if err != nil {
			return err
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("sendMessage %d: %s", resp.StatusCode, string(respBody))
		}
	}
	return nil
}
