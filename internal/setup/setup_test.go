package setup

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// feedStdin runs f() with os.Stdin replaced by the given input.
// Returns captured stdout.
func feedStdin(t *testing.T, input string, f func()) string {
	t.Helper()

	// Pipe stdin
	sinR, sinW, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = sinR

	// Pipe stdout
	soutR, soutW, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = soutW

	done := make(chan struct{})
	go func() {
		sinW.WriteString(input) //nolint:errcheck
		_ = sinW.Close()
		close(done)
	}()

	f()

	_ = soutW.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(soutR)
	_ = soutR.Close()

	os.Stdout = oldStdout
	os.Stdin = oldStdin
	_ = sinR.Close()
	<-done

	return buf.String()
}

// --- promptString ---

func TestPromptString_WithInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("my-value\n"))
	got := promptString(reader, "Enter", "")
	if got != "my-value" {
		t.Errorf("promptString with input = %q, want %q", got, "my-value")
	}
}

func TestPromptString_EmptyInputUsesFallback(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	got := promptString(reader, "Enter", "fallback")
	if got != "fallback" {
		t.Errorf("promptString empty input = %q, want %q", got, "fallback")
	}
}

func TestPromptString_NoFallback(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	got := promptString(reader, "Enter", "")
	if got != "" {
		t.Errorf("promptString empty no fallback = %q, want empty", got)
	}
}

// --- promptSecret ---

func TestPromptSecret_WithInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("sk-abc123\n"))
	got := promptSecret(reader, "API key", "")
	if got != "sk-abc123" {
		t.Errorf("promptSecret with input = %q, want %q", got, "sk-abc123")
	}
}

func TestPromptSecret_EmptyReturnsOriginal(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	got := promptSecret(reader, "API key", "sk-real-key")
	if got != "sk-real-key" {
		t.Errorf("promptSecret empty = %q, want %q", got, "sk-real-key")
	}
}

func TestPromptSecret_EmptyWithMaskedFallback(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	got := promptSecret(reader, "API key", "sk-1****cdef")
	// TrimPrefix only strips "****" if it's the actual prefix, not mid-string.
	// With a masked fallback like "sk-1****cdef", pressing enter returns
	// the masked string as-is since the real value isn't recoverable.
	if got != "sk-1****cdef" {
		t.Errorf("promptSecret masked = %q, want %q", got, "sk-1****cdef")
	}
}

func TestPromptSecret_EmptyNoFallback(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	got := promptSecret(reader, "API key", "")
	if got != "" {
		t.Errorf("promptSecret empty no fallback = %q, want empty", got)
	}
}

// --- promptInt ---

func TestPromptInt_Valid(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("3\n"))
	got := promptInt(reader, "Choose", 1, 5)
	if got != 3 {
		t.Errorf("promptInt = %d, want 3", got)
	}
}

func TestPromptInt_InvalidThenValid(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("abc\n2\n"))
	got := promptInt(reader, "Choose", 1, 5)
	if got != 2 {
		t.Errorf("promptInt after retry = %d, want 2", got)
	}
}

func TestPromptInt_Zero(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("0\n"))
	got := promptInt(reader, "Choose", 0, 5)
	if got != 0 {
		t.Errorf("promptInt zero = %d, want 0", got)
	}
}

func TestPromptInt_OutOfRangeHigh(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("99\n2\n"))
	got := promptInt(reader, "Choose", 1, 5)
	if got != 2 {
		t.Errorf("promptInt after out-of-range = %d, want 2", got)
	}
}

// --- promptYesNo ---

func TestPromptYesNo_DefaultYes(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	if !promptYesNo(reader, "Q", true) {
		t.Error("empty input with defaultYes=true should return true")
	}
}

func TestPromptYesNo_DefaultNo(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	if promptYesNo(reader, "Q", false) {
		t.Error("empty input with defaultYes=false should return false")
	}
}

func TestPromptYesNo_ExplicitYes(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("y\n"))
	if !promptYesNo(reader, "Q", false) {
		t.Error("'y' should return true")
	}
}

func TestPromptYesNo_ExplicitNo(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("n\n"))
	if promptYesNo(reader, "Q", true) {
		t.Error("'n' should return false")
	}
}

func TestPromptYesNo_FullWord(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("yes\n"))
	if !promptYesNo(reader, "Q", false) {
		t.Error("'yes' should return true")
	}
}

// --- generateToken ---

func TestGenerateToken_Uniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		token, err := generateToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[token] {
			t.Fatalf("duplicate token: %s", token)
		}
		seen[token] = true
	}
}

func TestGenerateToken_Format(t *testing.T) {
	token, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "pol_") {
		t.Fatalf("should start with 'pol_', got %q", token)
	}
	// "pol_" (4) + hex of 24 bytes (48) = 52
	if len(token) != 52 {
		t.Errorf("length = %d, want 52", len(token))
	}
	for _, c := range token[4:] {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			t.Errorf("unexpected char %q in hex portion", c)
			break
		}
	}
}

// --- maskKey ---

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "****"},
		{"a", "****"},
		{"short", "****"},
		{"abcd", "****"},
		{"abcdefgh", "****"},
		{"abcdefghi", "abcd****fghi"},
		{"sk-1234567890abcdef", "sk-1****cdef"},
	}
	for _, tc := range tests {
		got := maskKey(tc.input)
		if got != tc.want {
			t.Errorf("maskKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- defaultStr ---

func TestDefaultStr(t *testing.T) {
	if got := defaultStr("hello", "fallback"); got != "hello" {
		t.Errorf("with value = %q, want %q", got, "hello")
	}
	if got := defaultStr("", "fallback"); got != "fallback" {
		t.Errorf("without value = %q, want %q", got, "fallback")
	}
	if got := defaultStr("", ""); got != "" {
		t.Errorf("both empty = %q, want empty", got)
	}
}

// --- Providers ---

func TestProviders(t *testing.T) {
	if len(Providers) == 0 {
		t.Fatal("Providers list should not be empty")
	}
	for _, p := range Providers {
		if p.Name == "" {
			t.Error("Provider should have a name")
		}
		if p.Name != "Custom endpoint" && p.BaseURL == "" {
			t.Errorf("Provider %q should have a BaseURL", p.Name)
		}
	}
}

// --- Run integration tests ---

func TestRun_MinimalSetup(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// provider=1(OpenAI), model=1(gpt-4o-mini), api-key, telegram=n, image=n
	output := feedStdin(t, "1\n1\nsk-test-api-key-12345\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	_ = output

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	if !strings.Contains(content, "LLM_BASE_URL=https://api.openai.com/v1") {
		t.Errorf("env missing LLM_BASE_URL: %s", content)
	}
	if !strings.Contains(content, "LLM_MODEL=gpt-4o-mini") {
		t.Errorf("env missing LLM_MODEL: %s", content)
	}
	if !strings.Contains(content, "LLM_API_KEY=sk-test-api-key-12345") {
		t.Errorf("env missing LLM_API_KEY: %s", content)
	}
	if !strings.Contains(content, "AUTH_TOKEN=pol_") {
		t.Errorf("env missing generated AUTH_TOKEN: %s", content)
	}
	if strings.Contains(content, "TELEGRAM_BOT_TOKEN") {
		t.Errorf("env should not contain TELEGRAM_BOT_TOKEN: %s", content)
	}
	if strings.Contains(content, "IMAGE_CAPTION") {
		t.Errorf("env should not contain IMAGE_CAPTION: %s", content)
	}
}

func TestRun_OllamaProvider(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// provider=6(Ollama), model=1(llama3), telegram=n, image=n
	feedStdin(t, "6\n1\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	data, _ := os.ReadFile(envPath)
	content := string(data)

	if !strings.Contains(content, "LLM_BASE_URL=http://localhost:11434/v1") {
		t.Errorf("env missing Ollama URL: %s", content)
	}
	if !strings.Contains(content, "LLM_API_KEY=ollama") {
		t.Errorf("env should have placeholder key for Ollama: %s", content)
	}
}

func TestRun_CustomEndpoint(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// provider=7(Custom), base_url, model, api-key, telegram=n, image=n
	feedStdin(t, "7\nhttps://my-llm.example.com/v1\nmy-model-v2\nsk-custom-key\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	data, _ := os.ReadFile(envPath)
	content := string(data)

	if !strings.Contains(content, "LLM_BASE_URL=https://my-llm.example.com/v1") {
		t.Errorf("env missing custom URL: %s", content)
	}
	if !strings.Contains(content, "LLM_MODEL=my-model-v2") {
		t.Errorf("env missing custom model: %s", content)
	}
}

func TestRun_WithTelegram(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// provider=1, model=1, api-key, telegram=y, bot-token, image=n
	feedStdin(t, "1\n1\nsk-test-key\ny\n12345:bot-token-abc\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "TELEGRAM_BOT_TOKEN=12345:bot-token-abc") {
		t.Errorf("env missing TELEGRAM_BOT_TOKEN: %s", string(data))
	}
}

func TestRun_WithImageCaption(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// provider=1, model=1, api-key, telegram=n, image=y (defaults + custom key)
	feedStdin(t, "1\n1\nsk-test-key\nn\ny\n\n\nvision-key-123\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	data, _ := os.ReadFile(envPath)
	content := string(data)

	if !strings.Contains(content, "IMAGE_CAPTION_BASE_URL=https://generativelanguage.googleapis.com/v1beta/openai") {
		t.Errorf("env missing IMAGE_CAPTION_BASE_URL: %s", content)
	}
	if !strings.Contains(content, "IMAGE_CAPTION_MODEL=gemini-2.5-flash-lite") {
		t.Errorf("env missing IMAGE_CAPTION_MODEL: %s", content)
	}
}

func TestRun_PreservesExistingToken(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	_ = os.WriteFile(envPath, []byte("LLM_BASE_URL=https://existing.com\nLLM_API_KEY=existing-key\nAUTH_TOKEN=pol_existing_good_token\n"), 0o600)

	output := feedStdin(t, "1\n1\n\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	if !strings.Contains(output, "Found existing config") {
		t.Error("should mention existing config")
	}

	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "AUTH_TOKEN=pol_existing_good_token") {
		t.Errorf("existing auth token should be preserved: %s", string(data))
	}
}

func TestRun_EnvFilePermissions(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "subdir", ".env")

	feedStdin(t, "1\n1\nsk-test\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("env file perms = %o, want 0600", info.Mode().Perm())
	}
}

func TestRun_ManualModelEntry(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// provider=1(OpenAI), model=0(type manually), gpt-5-turbo, api-key, telegram=n, image=n
	feedStdin(t, "1\n0\ngpt-5-turbo\nsk-test\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	data, _ := os.ReadFile(envPath)
	if !strings.Contains(string(data), "LLM_MODEL=gpt-5-turbo") {
		t.Errorf("manual model not in env: %s", string(data))
	}
}

func TestRun_SummaryOutput(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	output := feedStdin(t, "1\n1\nsk-test\nn\nn\n", func() {
		err := Run(envPath)
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	})

	if !strings.Contains(output, "Setup complete") {
		t.Error("output should say Setup complete")
	}
	if !strings.Contains(output, "docker compose up") {
		t.Error("output should mention docker compose up")
	}
}
