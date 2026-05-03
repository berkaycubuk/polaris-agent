package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/agent"
	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/tools"
)

func testServer(t *testing.T) (*Server, *llm.Client) {
	t.Helper()
	dir := t.TempDir()
	client := &llm.Client{
		BaseURL: "https://fake-api.example.com",
		APIKey:  "test-key",
		Model:   "test-model",
		HTTP:    &http.Client{},
	}
	registry := tools.NewRegistry(dir)
	a := agent.New(client, registry, dir, agent.Options{MaxToolIterations: 1})
	return New(":0", "test-auth-token", a), client
}

func TestHealthz(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("expected 'ok', got %q", string(body))
	}
}

func TestRequireAuth_Valid(t *testing.T) {
	srv, _ := testServer(t)
	called := false
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer test-auth-token")
	w := httptest.NewRecorder()
	handler(w, req)
	if !called {
		t.Fatal("handler should have been called with valid token")
	}
}

func TestRequireAuth_Invalid(t *testing.T) {
	srv, _ := testServer(t)
	called := false
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	w := httptest.NewRecorder()
	handler(w, req)
	if called {
		t.Fatal("handler should not have been called with invalid token")
	}
	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Result().StatusCode)
	}
}

func TestRequireAuth_MissingHeader(t *testing.T) {
	srv, _ := testServer(t)
	handler := srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not be called")
	})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler(w, req)
	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Result().StatusCode)
	}
}

func TestChat_MethodNotAllowed(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest("GET", "/chat", nil)
	w := httptest.NewRecorder()
	srv.handleChat(w, req)
	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestChat_EmptyMessage(t *testing.T) {
	srv, _ := testServer(t)
	body, _ := json.Marshal(chatReq{Message: ""})
	req := httptest.NewRequest("POST", "/chat", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleChat(w, req)
	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestChat_InvalidJSON(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest("POST", "/chat", bytes.NewReader([]byte("not json")))
	w := httptest.NewRecorder()
	srv.handleChat(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestReset_MethodNotAllowed(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest("GET", "/reset", nil)
	w := httptest.NewRecorder()
	srv.handleReset(w, req)
	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestReset_Success(t *testing.T) {
	srv, _ := testServer(t)
	body, _ := json.Marshal(chatReq{Session: "s1"})
	req := httptest.NewRequest("POST", "/reset", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleReset(w, req)
	if w.Result().StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", w.Result().StatusCode)
	}
}

// Integration test: full chat round-trip with a fake LLM server.
func TestChat_Integration(t *testing.T) {
	dir := t.TempDir()

	// Stand up a fake LLM server that returns a simple text reply.
	fakeLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.ChatResponse{
			Choices: []struct {
				Message      llm.Message `json:"message"`
				FinishReason string      `json:"finish_reason"`
			}{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello back"}, FinishReason: "stop"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer fakeLLM.Close()

	client := llm.New(fakeLLM.URL, "test-key", "test-model")
	registry := tools.NewRegistry(dir)
	a := agent.New(client, registry, dir, agent.Options{MaxToolIterations: 5})
	srv := New(":0", "test-token", a)

	body, _ := json.Marshal(chatReq{Session: "s1", Message: "hello"})
	req := httptest.NewRequest("POST", "/chat", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	w := httptest.NewRecorder()

	// Use requireAuth + handleChat together
	srv.requireAuth(srv.handleChat)(w, req)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(raw))
	}

	var got chatResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Reply != "hello back" {
		t.Fatalf("expected 'hello back', got %q", got.Reply)
	}
}

// Test that the data dir is created on chat.
func TestChat_CreatesDataDirs(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "agent-data")

	fakeLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llm.ChatResponse{
			Choices: []struct {
				Message      llm.Message `json:"message"`
				FinishReason string      `json:"finish_reason"`
			}{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}, FinishReason: "stop"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer fakeLLM.Close()

	client := llm.New(fakeLLM.URL, "key", "model")
	registry := tools.NewRegistry(dataDir)
	a := agent.New(client, registry, dataDir, agent.Options{MaxToolIterations: 5})

	_, err := a.Chat(context.Background(), "test-session", "hi")
	if err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"", "wiki", "skills"} {
		p := filepath.Join(dataDir, sub)
		if stat, err := os.Stat(p); err != nil || !stat.IsDir() {
			t.Errorf("expected dir %q to exist", p)
		}
	}
}
