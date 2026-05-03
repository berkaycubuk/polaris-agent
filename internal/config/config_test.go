package config

import (
	"os"
	"testing"
)

func TestLoad_MissingRequired(t *testing.T) {
	// Clear any env from parent process.
	for _, k := range []string{
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
		"IMAGE_CAPTION_BASE_URL", "IMAGE_CAPTION_MODEL", "IMAGE_CAPTION_API_KEY",
		"R2_ACCOUNT_ID", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY",
	} {
		_ = os.Unsetenv(k)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for missing env vars")
	}
	for _, k := range []string{"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN"} {
		if !contains(err.Error(), k) {
			t.Errorf("expected %q in error message, got: %v", k, err)
		}
	}
}

func TestLoad_Success(t *testing.T) {
	setEnv(t,
		"LLM_BASE_URL", "https://api.example.com/v1",
		"LLM_MODEL", "gpt-4",
		"LLM_API_KEY", "sk-test-key",
		"AUTH_TOKEN", "tok-123",
	)
	defer unsetAll(t,
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMBaseURL != "https://api.example.com/v1" {
		t.Fatalf("got LLMBaseURL = %q", cfg.LLMBaseURL)
	}
	if cfg.LLMModel != "gpt-4" {
		t.Fatalf("got LLMModel = %q", cfg.LLMModel)
	}
	if cfg.DataDir == "" {
		t.Fatal("DataDir should default")
	}
	if cfg.HTTPAddr == "" {
		t.Fatal("HTTPAddr should default")
	}
	if cfg.MaxToolIterations <= 0 {
		t.Fatal("MaxToolIterations should be positive")
	}
}

func TestLoad_Defaults(t *testing.T) {
	setEnv(t,
		"LLM_BASE_URL", "x",
		"LLM_MODEL", "x",
		"LLM_API_KEY", "x",
		"AUTH_TOKEN", "x",
	)
	defer unsetAll(t,
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
		"DATA_DIR", "HTTP_ADDR", "MAX_TOOL_ITERATIONS",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/app/data" {
		t.Fatalf("default DataDir = %q, want /app/data", cfg.DataDir)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("default HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.MaxToolIterations != 30 {
		t.Fatalf("default MaxToolIterations = %d, want 30", cfg.MaxToolIterations)
	}
}

func TestLoad_CustomOptionalFields(t *testing.T) {
	setEnv(t,
		"LLM_BASE_URL", "x",
		"LLM_MODEL", "x",
		"LLM_API_KEY", "x",
		"AUTH_TOKEN", "x",
		"DATA_DIR", "/custom",
		"HTTP_ADDR", ":9090",
		"MAX_TOOL_ITERATIONS", "50",
	)
	defer unsetAll(t,
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
		"DATA_DIR", "HTTP_ADDR", "MAX_TOOL_ITERATIONS",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != "/custom" {
		t.Fatalf("got DataDir = %q", cfg.DataDir)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Fatalf("got HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.MaxToolIterations != 50 {
		t.Fatalf("got MaxToolIterations = %d", cfg.MaxToolIterations)
	}
}

func TestLoad_PartialCaptionConfig(t *testing.T) {
	setEnv(t,
		"LLM_BASE_URL", "x",
		"LLM_MODEL", "x",
		"LLM_API_KEY", "x",
		"AUTH_TOKEN", "x",
		"IMAGE_CAPTION_BASE_URL", "https://caption.example.com",
	)
	defer unsetAll(t,
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
		"IMAGE_CAPTION_BASE_URL",
	)

	_, err := Load()
	if err == nil || !contains(err.Error(), "IMAGE_CAPTION_*") {
		t.Fatalf("expected partial config error, got: %v", err)
	}
}

func TestLoad_PartialR2Config(t *testing.T) {
	setEnv(t,
		"LLM_BASE_URL", "x",
		"LLM_MODEL", "x",
		"LLM_API_KEY", "x",
		"AUTH_TOKEN", "x",
		"R2_ACCOUNT_ID", "acc123",
		"R2_BUCKET", "my-bucket",
	)
	defer unsetAll(t,
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
		"R2_ACCOUNT_ID", "R2_BUCKET",
	)

	_, err := Load()
	if err == nil || !contains(err.Error(), "R2_*") {
		t.Fatalf("expected partial R2 config error, got: %v", err)
	}
}

func TestLoad_FullCaptionAndR2(t *testing.T) {
	setEnv(t,
		"LLM_BASE_URL", "x",
		"LLM_MODEL", "x",
		"LLM_API_KEY", "x",
		"AUTH_TOKEN", "x",
		"IMAGE_CAPTION_BASE_URL", "https://cap.example.com",
		"IMAGE_CAPTION_MODEL", "gemini-flash",
		"IMAGE_CAPTION_API_KEY", "cap-key",
		"R2_ACCOUNT_ID", "acc",
		"R2_BUCKET", "bkt",
		"R2_ACCESS_KEY_ID", "ak",
		"R2_SECRET_ACCESS_KEY", "sk",
		"R2_PUBLIC_BASE_URL", "https://cdn.example.com",
	)
	defer unsetAll(t,
		"LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "AUTH_TOKEN",
		"IMAGE_CAPTION_BASE_URL", "IMAGE_CAPTION_MODEL", "IMAGE_CAPTION_API_KEY",
		"R2_ACCOUNT_ID", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_PUBLIC_BASE_URL",
	)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ImageCaptionEnabled() {
		t.Fatal("captioner should be enabled")
	}
	if !cfg.R2Enabled() {
		t.Fatal("R2 should be enabled")
	}
	if cfg.R2PublicBaseURL != "https://cdn.example.com" {
		t.Fatalf("got R2PublicBaseURL = %q", cfg.R2PublicBaseURL)
	}
}

func TestImageCaptionEnabled_Partial(t *testing.T) {
	cfg := &Config{
		ImageCaptionBaseURL: "url",
		ImageCaptionModel:   "model",
		// missing APIKey
	}
	if cfg.ImageCaptionEnabled() {
		t.Fatal("should be disabled with partial config")
	}
}

func TestR2Enabled_Partial(t *testing.T) {
	cfg := &Config{
		R2AccountID: "acc",
		R2Bucket:    "bkt",
		// missing AccessKeyID and SecretKey
	}
	if cfg.R2Enabled() {
		t.Fatal("should be disabled with partial config")
	}
}

func TestGetenvInt_Invalid(t *testing.T) {
	t.Setenv("TEST_BAD_INT", "notanumber")
	if got := getenvInt("TEST_BAD_INT", 42); got != 42 {
		t.Fatalf("expected fallback 42, got %d", got)
	}
}

func TestGetenvInt_Zero(t *testing.T) {
	t.Setenv("TEST_ZERO_INT", "0")
	if got := getenvInt("TEST_ZERO_INT", 42); got != 42 {
		t.Fatalf("zero should use fallback, got %d", got)
	}
}

func TestGetenvInt_Negative(t *testing.T) {
	t.Setenv("TEST_NEG_INT", "-5")
	if got := getenvInt("TEST_NEG_INT", 42); got != 42 {
		t.Fatalf("negative should use fallback, got %d", got)
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	envFile := dir + "/.env"
	content := "DOT_ENV_TEST_VAR=from_dotenv\n# comment\n\nDOT_ENV_OTHER=hello\n"
	if err := os.WriteFile(envFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOT_ENV_TEST_VAR", "")
	_ = os.Unsetenv("DOT_ENV_TEST_VAR")
	t.Setenv("DOT_ENV_OTHER", "")
	_ = os.Unsetenv("DOT_ENV_OTHER")

	// Change working dir so .env is found
	origDir, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origDir) }()

	loadDotEnv(".env")
	if os.Getenv("DOT_ENV_TEST_VAR") != "from_dotenv" {
		t.Fatalf("expected DOT_ENV_TEST_VAR=from_dotenv, got %q", os.Getenv("DOT_ENV_TEST_VAR"))
	}
	if os.Getenv("DOT_ENV_OTHER") != "hello" {
		t.Fatalf("expected DOT_ENV_OTHER=hello, got %q", os.Getenv("DOT_ENV_OTHER"))
	}
}

func TestLoadDotEnv_DoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DOT_NO_OVERRIDE", "original")

	envFile := dir + "/.env"
	if err := os.WriteFile(envFile, []byte("DOT_NO_OVERRIDE=from_file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origDir) }()

	loadDotEnv(".env")
	if os.Getenv("DOT_NO_OVERRIDE") != "original" {
		t.Fatalf("existing env should not be overridden, got %q", os.Getenv("DOT_NO_OVERRIDE"))
	}
}

func TestValidateGroup(t *testing.T) {
	tests := []struct {
		name    string
		vals    []string
		wantErr bool
	}{
		{"all empty", []string{"", "", ""}, false},
		{"all set", []string{"a", "b", "c"}, false},
		{"partial", []string{"a", "", "c"}, true},
		{"first only", []string{"a", "", ""}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateGroup("TEST", tc.vals...)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateGroup() = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// helpers

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || containsSub(s, sub))
}

func containsSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func setEnv(t *testing.T, pairs ...string) {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		t.Setenv(pairs[i], pairs[i+1])
	}
}

func unsetAll(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		_ = os.Unsetenv(k)
	}
}
