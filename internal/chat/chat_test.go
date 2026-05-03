package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestSendMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/chat" {
			t.Errorf("expected /chat, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if v := r.Header.Get("Authorization"); v != "Bearer test-token" {
			t.Errorf("expected Bearer test-token, got %s", v)
		}

		var req chatReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.Session != "cli" {
			t.Errorf("expected session cli, got %s", req.Session)
		}
		if req.Message != "hello" {
			t.Errorf("expected message hello, got %s", req.Message)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"reply": "Hi there!"})
	}))
	defer server.Close()

	opts := Options{
		ServerURL: server.URL,
		AuthToken: "test-token",
		Session:   "cli",
	}

	reply, err := sendMessage(opts, "hello")
	if err != nil {
		t.Fatalf("sendMessage: %v", err)
	}
	if reply != "Hi there!" {
		t.Errorf("expected 'Hi there!', got %q", reply)
	}
}

func TestSendMessageUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	opts := Options{
		ServerURL: server.URL,
		AuthToken: "bad-token",
		Session:   "cli",
	}

	_, err := sendMessage(opts, "hello")
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
}

func TestResetSession(t *testing.T) {
	session := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reset" && r.Method == http.MethodPost {
			var req chatReq
			json.NewDecoder(r.Body).Decode(&req)
			session = req.Session
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}))
	defer server.Close()

	opts := Options{
		ServerURL: server.URL,
		AuthToken: "test-token",
		Session:   "cli",
	}

	if err := resetSession(opts); err != nil {
		t.Fatalf("resetSession: %v", err)
	}
	if session != "cli" {
		t.Errorf("expected session cli, got %s", session)
	}
}

func TestHealthCheck(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	if err := healthCheck(server.URL); err != nil {
		t.Fatalf("healthCheck: %v", err)
	}
}

func TestHealthCheckFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if err := healthCheck(server.URL); err == nil {
		t.Fatal("expected error for unhealthy server")
	}
}

func TestLoadChatOpts(t *testing.T) {
	// Create a temp .env file
	tmpDir := t.TempDir()
	envPath := tmpDir + "/.env"
	content := "AUTH_TOKEN=mytoken\nHTTP_ADDR=:9090\nPOLARIS_SESSION=test-session\n"
	if err := writeTestFile(envPath, content); err != nil {
		t.Fatal(err)
	}

	opts := LoadChatOpts(envPath)

	if opts.AuthToken != "mytoken" {
		t.Errorf("expected auth token mytoken, got %s", opts.AuthToken)
	}
	if opts.ServerURL != "http://localhost:9090" {
		t.Errorf("expected http://localhost:9090, got %s", opts.ServerURL)
	}
	if opts.Session != "test-session" {
		t.Errorf("expected test-session, got %s", opts.Session)
	}
}

func TestLoadChatOptsDefaults(t *testing.T) {
	// Non-existent file should give defaults
	opts := LoadChatOpts("/nonexistent/.env")

	if opts.ServerURL != "http://localhost:8080" {
		t.Errorf("expected http://localhost:8080, got %s", opts.ServerURL)
	}
	if opts.AuthToken != "" {
		t.Errorf("expected empty auth token, got %s", opts.AuthToken)
	}
	if opts.Session != "" {
		t.Errorf("expected empty session, got %s", opts.Session)
	}
}

func TestWrapLine(t *testing.T) {
	tests := []struct {
		name   string
		line   string
		width  int
		indent int
		want   int // expected number of wrapped lines
	}{
		{"short line", "hello", 76, 2, 1},
		{"exact width", "a b c d e f g h i j k l m n o p q r s t u v w x y z", 30, 2, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapLine(tt.line, tt.width, tt.indent)
			if len(got) != tt.want {
				t.Errorf("wrapLine returned %d lines, want %d: %v", len(got), tt.want, got)
			}
		})
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
