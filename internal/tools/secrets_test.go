package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretRedactor_EnvFiltering(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/polaris")
	t.Setenv("LLM_API_KEY", "sk-this-is-a-fake-llm-key-1234567890")
	t.Setenv("AUTH_TOKEN", "auth-12345678901234567890")
	t.Setenv("SKILL_SPOTIFY_TOKEN", "spotify-refresh-token-abcdefghij")
	t.Setenv("SKILL_NOTION", "notion-secret-blob-1234567890")
	t.Setenv("RANDOM_VAR", "should-not-be-passed-and-not-redacted")

	r := newSecretRedactor("")

	envSet := map[string]string{}
	for _, kv := range r.ChildEnv() {
		eq := strings.IndexByte(kv, '=')
		envSet[kv[:eq]] = kv[eq+1:]
	}

	// SKILL_* must pass through.
	if envSet["SKILL_SPOTIFY_TOKEN"] != "spotify-refresh-token-abcdefghij" {
		t.Fatalf("SKILL_SPOTIFY_TOKEN missing from child env: %v", envSet)
	}
	if envSet["SKILL_NOTION"] != "notion-secret-blob-1234567890" {
		t.Fatalf("SKILL_NOTION missing from child env: %v", envSet)
	}

	// Internal secrets must NOT pass through.
	for _, k := range []string{"LLM_API_KEY", "AUTH_TOKEN"} {
		if _, ok := envSet[k]; ok {
			t.Fatalf("%s should not have leaked to child env", k)
		}
	}

	// Allow-listed system vars pass through.
	if envSet["PATH"] == "" {
		t.Fatal("PATH should pass through")
	}
	if envSet["HOME"] == "" {
		t.Fatal("HOME should pass through")
	}

	// RANDOM_VAR (no SKILL_ prefix, no secret-substring) should NOT pass.
	if _, ok := envSet["RANDOM_VAR"]; ok {
		t.Fatal("RANDOM_VAR should not pass to child env")
	}
}

func TestSecretRedactor_RedactsKnownValues(t *testing.T) {
	t.Setenv("LLM_API_KEY", "sk-this-is-a-fake-llm-key-1234567890")
	t.Setenv("SKILL_SPOTIFY_TOKEN", "spotify-refresh-token-abcdefghij")
	// Short value (under floor) should NOT be redacted.
	t.Setenv("SKILL_TINY", "shortone")

	r := newSecretRedactor("")

	tests := []struct {
		in       string
		mustHave string
		mustNot  string
	}{
		{
			in:       "got LLM_API_KEY=sk-this-is-a-fake-llm-key-1234567890 in env",
			mustHave: "[REDACTED]",
			mustNot:  "sk-this-is-a-fake-llm-key-1234567890",
		},
		{
			in:       "spotify token: spotify-refresh-token-abcdefghij here",
			mustHave: "[REDACTED]",
			mustNot:  "spotify-refresh-token-abcdefghij",
		},
		{
			// Short value: not redacted (would be too false-positive prone).
			in:       "tiny=shortone here",
			mustHave: "shortone",
		},
	}

	for _, tc := range tests {
		got := r.Redact(tc.in)
		if !strings.Contains(got, tc.mustHave) {
			t.Errorf("Redact(%q) = %q, want substring %q", tc.in, got, tc.mustHave)
		}
		if tc.mustNot != "" && strings.Contains(got, tc.mustNot) {
			t.Errorf("Redact(%q) still contains forbidden %q: %q", tc.in, tc.mustNot, got)
		}
	}
}

func TestSecretRedactor_FilesInSecretsDir(t *testing.T) {
	dir := t.TempDir()
	secretsDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// JSON-shaped secret: whole file content is the value.
	jsonContent := `{"refresh_token":"google-oauth-refresh-token-very-long-12345"}`
	if err := os.WriteFile(filepath.Join(secretsDir, "google.json"), []byte(jsonContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// .env-shaped secret: per-line values are also redacted.
	envContent := "FOO=this-is-a-fake-foo-token-123456\nBAR=another-fake-bar-12345678\n"
	if err := os.WriteFile(filepath.Join(secretsDir, "service.env"), []byte(envContent), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newSecretRedactor(secretsDir)

	cases := []string{
		jsonContent,
		"echo this-is-a-fake-foo-token-123456",
		"echo another-fake-bar-12345678",
	}
	for _, in := range cases {
		got := r.Redact(in)
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("expected redaction for %q, got %q", in, got)
		}
	}
}

func TestSecretRedactor_PublicPrefixNotRedacted(t *testing.T) {
	t.Setenv("SKILL_PUBLIC_GOOGLE_CLIENT_ID", "1234567890-public-client-id.apps.googleusercontent.com")
	t.Setenv("SKILL_GOOGLE_CLIENT_SECRET", "GOCSPX-secret-value-redact-this-please")

	r := newSecretRedactor("")

	// Public var passes to children.
	envSet := map[string]string{}
	for _, kv := range r.ChildEnv() {
		eq := strings.IndexByte(kv, '=')
		envSet[kv[:eq]] = kv[eq+1:]
	}
	if envSet["SKILL_PUBLIC_GOOGLE_CLIENT_ID"] == "" {
		t.Fatal("SKILL_PUBLIC_GOOGLE_CLIENT_ID must reach child env")
	}
	if envSet["SKILL_GOOGLE_CLIENT_SECRET"] == "" {
		t.Fatal("SKILL_GOOGLE_CLIENT_SECRET must reach child env")
	}

	// Public name listed under PublicEnvNames, not SkillEnvNames.
	pub := r.PublicEnvNames()
	if len(pub) != 1 || pub[0] != "SKILL_PUBLIC_GOOGLE_CLIENT_ID" {
		t.Fatalf("PublicEnvNames = %v", pub)
	}
	skill := r.SkillEnvNames()
	if len(skill) != 1 || skill[0] != "SKILL_GOOGLE_CLIENT_SECRET" {
		t.Fatalf("SkillEnvNames = %v (public should be excluded)", skill)
	}

	// Sample auth URL: client_id stays visible, client_secret redacted.
	url := "https://accounts.google.com/o/oauth2/auth?client_id=1234567890-public-client-id.apps.googleusercontent.com&secret_for_test=GOCSPX-secret-value-redact-this-please"
	got := r.Redact(url)
	if !strings.Contains(got, "1234567890-public-client-id.apps.googleusercontent.com") {
		t.Fatalf("public client_id was redacted from URL: %s", got)
	}
	if strings.Contains(got, "GOCSPX-secret-value-redact-this-please") {
		t.Fatalf("client_secret leaked: %s", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected secret to be replaced with [REDACTED]: %s", got)
	}
}

func TestSecretRedactor_NilSafe(t *testing.T) {
	var r *secretRedactor
	if got := r.Redact("hello"); got != "hello" {
		t.Fatalf("nil redactor must pass through: %q", got)
	}
}
