package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/agent"
	"github.com/berkaycubuk/polaris-agent/internal/attachment"
)

const apiBase = "https://api.telegram.org/bot"

type Bot struct {
	token      string
	agent      *agent.Agent
	http       *http.Client
	allowedIDs []int64 // explicit allowlist from TELEGRAM_ALLOWED_USERS
	ownerFile  string  // path to persist auto-detected owner
	msgs       *msgCache
}

func New(token string, a *agent.Agent, allowedIDs []int64, ownerFile string) *Bot {
	return &Bot{
		token:      token,
		agent:      a,
		http:       &http.Client{Timeout: 70 * time.Second},
		allowedIDs: allowedIDs,
		ownerFile:  ownerFile,
		msgs:       newMsgCache(500),
	}
}

// msgRecord captures enough about a Telegram message to quote it back when
// the user later reacts to it. fromBot tells us whether to frame the quote
// as "your earlier message" (agent) or "my earlier message" (user).
type msgRecord struct {
	text    string
	fromBot bool
}

// msgCache is a tiny FIFO cache of recent messages keyed by (chat_id,
// message_id). Bounded so a busy chat can't grow unbounded across a long
// uptime; older entries fall out and reactions on them quote the message
// generically.
type msgCache struct {
	mu    sync.Mutex
	data  map[string]msgRecord
	order []string
	cap   int
}

func newMsgCache(capacity int) *msgCache {
	return &msgCache{data: map[string]msgRecord{}, cap: capacity}
}

func msgKey(chatID int64, msgID int) string {
	return strconv.FormatInt(chatID, 10) + ":" + strconv.Itoa(msgID)
}

func (c *msgCache) put(chatID int64, msgID int, rec msgRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := msgKey(chatID, msgID)
	if _, ok := c.data[k]; !ok {
		c.order = append(c.order, k)
		if len(c.order) > c.cap {
			delete(c.data, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.data[k] = rec
}

func (c *msgCache) get(chatID int64, msgID int) (msgRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.data[msgKey(chatID, msgID)]
	return rec, ok
}

type update struct {
	UpdateID        int                     `json:"update_id"`
	Message         *message                `json:"message"`
	MessageReaction *messageReactionUpdated `json:"message_reaction"`
}

// messageReactionUpdated is the payload Telegram sends when a user adds or
// removes an emoji reaction on a message. Requires opting in via
// allowed_updates=["message","message_reaction"] on getUpdates.
type messageReactionUpdated struct {
	Chat        chat           `json:"chat"`
	MessageID   int            `json:"message_id"`
	User        *user          `json:"user"`
	OldReaction []reactionType `json:"old_reaction"`
	NewReaction []reactionType `json:"new_reaction"`
}

type reactionType struct {
	Type     string `json:"type"`            // "emoji" or "custom_emoji"
	Emoji    string `json:"emoji,omitempty"` // present when type == "emoji"
	CustomID string `json:"custom_emoji_id,omitempty"`
}

type message struct {
	MessageID      int         `json:"message_id"`
	Chat           chat        `json:"chat"`
	From           *user       `json:"from"`
	Text           string      `json:"text"`
	Caption        string      `json:"caption"`
	Photo          []photoSize `json:"photo"`
	Document       *document   `json:"document"`
	ReplyToMessage *message    `json:"reply_to_message,omitempty"`
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
	if len(b.allowedIDs) > 0 {
		log.Printf("telegram bot started (explicit allowlist: %d user(s))", len(b.allowedIDs))
	} else {
		owner := b.readOwner()
		if owner != 0 {
			log.Printf("telegram bot started (auto-detected owner: %d)", owner)
		} else {
			log.Printf("telegram bot started (no owner yet — first messenger will be auto-claimed)")
		}
	}
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
			if u.MessageReaction != nil {
				b.handleReaction(u.MessageReaction)
				continue
			}
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

// handleReaction notes a Telegram emoji reaction in the agent's session
// history without triggering a reply. Only additions are recorded — removing
// a reaction would just create chatter. The reacted-to message is looked up
// in the per-chat cache so the agent sees a quoted excerpt and knows which
// side spoke ("your" for agent messages, "my" for the user's own).
func (b *Bot) handleReaction(r *messageReactionUpdated) {
	if r == nil {
		return
	}
	if !b.isAllowed(r.Chat.ID) {
		return
	}
	added := newEmojis(r.OldReaction, r.NewReaction)
	if len(added) == 0 {
		return
	}
	session := "telegram:" + strconv.FormatInt(r.Chat.ID, 10)
	emojis := strings.Join(added, "")

	rec, ok := b.msgs.get(r.Chat.ID, r.MessageID)
	var note string
	switch {
	case !ok:
		// Message fell out of the cache (or predates the bot's uptime).
		note = fmt.Sprintf("[I reacted %s to one of your earlier messages]", emojis)
	case rec.text == "":
		who := whoseMessage(rec.fromBot)
		note = fmt.Sprintf("[I reacted %s to %s earlier message]", emojis, who)
	default:
		who := whoseMessage(rec.fromBot)
		note = fmt.Sprintf("[I reacted %s to %s earlier message: \"%s\"]", emojis, who, excerpt(rec.text, 200))
	}
	b.agent.Observe(session, note)
}

func whoseMessage(fromBot bool) string {
	if fromBot {
		return "your"
	}
	return "my"
}

// newEmojis returns the emoji reactions present in cur but not in prev.
// Custom emojis are skipped — we only have the ID, not the visible glyph.
func newEmojis(prev, cur []reactionType) []string {
	seen := map[string]bool{}
	for _, p := range prev {
		if p.Type == "emoji" {
			seen[p.Emoji] = true
		}
	}
	var out []string
	for _, c := range cur {
		if c.Type != "emoji" || c.Emoji == "" {
			continue
		}
		if !seen[c.Emoji] {
			out = append(out, c.Emoji)
		}
	}
	return out
}

func (b *Bot) handleMessage(ctx context.Context, m *message) {
	chatID := m.Chat.ID

	if !b.isAllowed(chatID) {
		log.Printf("telegram: unauthorized chat_id=%d", chatID)
		_ = b.send(ctx, chatID, "Unauthorized.")
		return
	}

	session := "telegram:" + strconv.FormatInt(chatID, 10)

	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}

	// Remember the incoming message so a later reaction on it can be quoted
	// back to the agent. Photos with no caption still get an empty entry so
	// the message_id is at least known.
	b.msgs.put(chatID, m.MessageID, msgRecord{text: text, fromBot: false})

	if text == "/start" {
		_ = b.send(ctx, chatID, "Hi, I'm Polaris. Send me anything — text or photos.")
		return
	}
	if text == "/reset" {
		b.agent.Reset(session)
		_ = b.send(ctx, chatID, "Session cleared.")
		return
	}

	if prefix := replyPrefix(m); prefix != "" {
		if text == "" {
			text = prefix
		} else {
			text = prefix + "\n\n" + text
		}
	}

	// Show "typing..." for the entire request, including any attachment
	// downloads. Telegram's chat action auto-expires after ~5s, so we
	// refresh on a ticker until the reply is sent.
	typingCtx, stopTyping := context.WithCancel(ctx)
	defer stopTyping()
	go b.pumpTyping(typingCtx, chatID)

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
	stopTyping()
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

// isAllowed checks whether chatID is authorized. If an explicit allowlist
// is configured (TELEGRAM_ALLOWED_USERS), it takes precedence. Otherwise,
// the first user to message the bot is auto-claimed as the owner and
// persisted to disk so it survives restarts.
func (b *Bot) isAllowed(chatID int64) bool {
	// Explicit allowlist overrides auto-detect.
	if len(b.allowedIDs) > 0 {
		for _, id := range b.allowedIDs {
			if id == chatID {
				return true
			}
		}
		return false
	}

	// Auto-detect mode: check persisted owner.
	owner := b.readOwner()
	if owner != 0 {
		return owner == chatID
	}

	// No owner yet — this user claims it.
	b.writeOwner(chatID)
	log.Printf("telegram: auto-claimed owner chat_id=%d", chatID)
	return true
}

func (b *Bot) readOwner() int64 {
	if b.ownerFile == "" {
		return 0
	}
	data, err := os.ReadFile(b.ownerFile)
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

func (b *Bot) writeOwner(id int64) {
	if b.ownerFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(b.ownerFile), 0o755); err != nil {
		log.Printf("telegram: failed to create owner dir: %v", err)
		return
	}
	if err := os.WriteFile(b.ownerFile, []byte(strconv.FormatInt(id, 10)+"\n"), 0o644); err != nil {
		log.Printf("telegram: failed to write owner file: %v", err)
	}
}

// pumpTyping keeps the "typing..." indicator visible while the agent
// processes. Telegram's sendChatAction status expires after ~5 seconds,
// so we re-send every 4. Returns when ctx is cancelled.
func (b *Bot) pumpTyping(ctx context.Context, chatID int64) {
	if err := b.sendChatAction(ctx, chatID, "typing"); err != nil && ctx.Err() == nil {
		log.Printf("telegram chatAction: %v", err)
	}
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := b.sendChatAction(ctx, chatID, "typing"); err != nil && ctx.Err() == nil {
				log.Printf("telegram chatAction: %v", err)
			}
		}
	}
}

func (b *Bot) sendChatAction(ctx context.Context, chatID int64, action string) error {
	body := map[string]any{"chat_id": chatID, "action": action}
	data, _ := json.Marshal(body)
	u := apiBase + b.token + "/sendChatAction"
	req, err := http.NewRequestWithContext(ctx, "POST", u, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.http.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("sendChatAction %d", resp.StatusCode)
	}
	return nil
}

// collectAttachments downloads any image attachments on the message and
// returns them as agent.Attachments. Photos pick the largest size; documents
// are included only if their MIME type starts with "image/".
func (b *Bot) collectAttachments(ctx context.Context, m *message) ([]attachment.Attachment, error) {
	var atts []attachment.Attachment
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
		atts = append(atts, attachment.Attachment{Data: data, MimeType: mime})
	}
	if m.Document != nil && strings.HasPrefix(m.Document.MimeType, "image/") {
		data, _, err := b.downloadFile(ctx, m.Document.FileID)
		if err != nil {
			return nil, err
		}
		atts = append(atts, attachment.Attachment{Data: data, MimeType: m.Document.MimeType})
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
	_ = resp.Body.Close()
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
	defer func() { _ = dresp.Body.Close() }()
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

// replyPrefix builds a one-line context note when the user is replying to a
// prior message in the chat. Frames the agent in second person ("your") and
// the user in first person ("my") so the agent can tell which side spoke.
// Returns "" when there's no quotable text on the replied-to message.
func replyPrefix(m *message) string {
	r := m.ReplyToMessage
	if r == nil {
		return ""
	}
	quoted := strings.TrimSpace(r.Text)
	if quoted == "" {
		quoted = strings.TrimSpace(r.Caption)
	}
	if quoted == "" {
		return ""
	}
	who := "your"
	if r.From != nil && m.From != nil && r.From.ID == m.From.ID {
		who = "my"
	}
	return fmt.Sprintf("[Replying to %s earlier message: \"%s\"]", who, excerpt(quoted, 200))
}

// excerpt collapses internal whitespace and truncates s to at most max runes,
// appending an ellipsis when truncated.
func excerpt(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
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
	// Telegram's default allowed_updates excludes message_reaction; opt in
	// explicitly. Listing "message" too because once we set the param we're
	// no longer on defaults.
	q.Set("allowed_updates", `["message","message_reaction"]`)
	u := apiBase + b.token + "/getUpdates?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
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

// Send pushes text to chatID, splitting on Telegram's 4096-char limit.
// Used by external callers (e.g. the scheduler delivering cron-job replies).
func (b *Bot) Send(ctx context.Context, chatID int64, text string) error {
	return b.send(ctx, chatID, text)
}

// Telegram caps message length at 4096 chars; chunk longer responses.
// Each successfully-sent chunk is cached with its message_id so a later
// reaction can quote which chunk the user reacted to.
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
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("sendMessage %d: %s", resp.StatusCode, string(respBody))
		}
		var sr struct {
			Result struct {
				MessageID int `json:"message_id"`
			} `json:"result"`
		}
		if err := json.Unmarshal(respBody, &sr); err == nil && sr.Result.MessageID != 0 {
			b.msgs.put(chatID, sr.Result.MessageID, msgRecord{text: piece, fromBot: true})
		}
	}
	return nil
}
