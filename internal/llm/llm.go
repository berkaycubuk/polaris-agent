package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is a single chat message. Content is a plain string for most
// turns; for multimodal user input set ContentParts (text + image_url
// blocks) and leave Content empty. Marshaling chooses the right wire
// format automatically.
type Message struct {
	Role         Role       `json:"-"`
	Content      string     `json:"-"`
	ContentParts []Part     `json:"-"`
	ToolCalls    []ToolCall `json:"-"`
	ToolCallID   string     `json:"-"`
	Name         string     `json:"-"`
}

// Part is one block of multimodal content. Type is "text" or "image_url".
type Part struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

type ImageURL struct {
	URL string `json:"url"`
}

type messageWire struct {
	Role       Role            `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	w := messageWire{
		Role:       m.Role,
		ToolCalls:  m.ToolCalls,
		ToolCallID: m.ToolCallID,
		Name:       m.Name,
	}
	switch {
	case len(m.ContentParts) > 0:
		b, err := json.Marshal(m.ContentParts)
		if err != nil {
			return nil, err
		}
		w.Content = b
	case m.Content != "" || len(m.ToolCalls) == 0:
		// Always emit content for non-tool-call assistant messages and
		// for system/user/tool turns; omit only when an assistant turn is
		// purely tool calls with empty text.
		b, _ := json.Marshal(m.Content)
		w.Content = b
	}
	return json.Marshal(w)
}

func (m *Message) UnmarshalJSON(data []byte) error {
	var w messageWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	m.Role = w.Role
	m.ToolCalls = w.ToolCalls
	m.ToolCallID = w.ToolCallID
	m.Name = w.Name
	if len(w.Content) == 0 || string(w.Content) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(w.Content, &s); err == nil {
		m.Content = s
		return nil
	}
	var parts []Part
	if err := json.Unmarshal(w.Content, &parts); err == nil {
		m.ContentParts = parts
		return nil
	}
	return fmt.Errorf("llm: unrecognized content format: %s", string(w.Content))
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function ToolFunc `json:"function"`
}

type ToolFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []Tool    `json:"tools,omitempty"`
}

type ChatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type Client struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func New(baseURL, apiKey, model string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *Client) Chat(ctx context.Context, messages []Message, tools []Tool) (*Message, error) {
	body, err := json.Marshal(ChatRequest{Model: c.Model, Messages: messages, Tools: tools})
	if err != nil {
		return nil, err
	}
	url := c.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, string(raw))
	}
	var out ChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode llm: %w (body=%s)", err, string(raw))
	}
	if out.Error != nil {
		return nil, fmt.Errorf("llm: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("llm: no choices in response")
	}
	return &out.Choices[0].Message, nil
}
