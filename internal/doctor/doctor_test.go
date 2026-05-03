package doctor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- ParseEnvFileFromReader edge cases ---

func TestParseEnvFileFromReader_CommentsAndBlanks(t *testing.T) {
	input := "# top comment\n\n  # indented comment\nKEY=val\n\n"
	vars := ParseEnvFileFromReader(strings.NewReader(input))
	if vars["KEY"] != "val" {
		t.Errorf("KEY = %q, want %q", vars["KEY"], "val")
	}
	if len(vars) != 1 {
		t.Errorf("expected 1 var, got %d", len(vars))
	}
}

func TestParseEnvFileFromReader_NoEquals(t *testing.T) {
	input := "NOEQUALS\nKEY=val\n"
	vars := ParseEnvFileFromReader(strings.NewReader(input))
	if _, ok := vars["NOEQUALS"]; ok {
		t.Error("lines without '=' should be skipped")
	}
	if vars["KEY"] != "val" {
		t.Errorf("KEY = %q", vars["KEY"])
	}
}

func TestParseEnvFileFromReader_EqualsInValue(t *testing.T) {
	input := "KEY=val=with=equals\n"
	vars := ParseEnvFileFromReader(strings.NewReader(input))
	if vars["KEY"] != "val=with=equals" {
		t.Errorf("KEY = %q, want %q", vars["KEY"], "val=with=equals")
	}
}

func TestParseEnvFileFromReader_SpacesAroundEquals(t *testing.T) {
	input := "  KEY  =  value  \n"
	vars := ParseEnvFileFromReader(strings.NewReader(input))
	if vars["KEY"] != "value" {
		t.Errorf("KEY = %q, want %q", vars["KEY"], "value")
	}
}

// --- parseEnvFile (from actual file) ---

func TestParseEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "ONE=1\nTWO=two\n"
	_ = os.WriteFile(path, []byte(content), 0o644)

	vars := parseEnvFile(path)
	if vars["ONE"] != "1" {
		t.Errorf("ONE = %q", vars["ONE"])
	}
	if vars["TWO"] != "two" {
		t.Errorf("TWO = %q", vars["TWO"])
	}
}

func TestParseEnvFile_Missing(t *testing.T) {
	vars := parseEnvFile("/tmp/does-not-exist-doctor-test")
	if len(vars) != 0 {
		t.Errorf("expected empty vars for missing file, got %d", len(vars))
	}
}

// --- maskKey ---

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sk-1234567890abcdef", "sk-1****cdef"},
		{"short", "****"},
		{"", "****"},
		{"abcd", "****"},
		{"abcdefgh", "****"},
		{"abcdefghi", "abcd****fghi"},
	}
	for _, tc := range tests {
		got := maskKey(tc.input)
		if got != tc.want {
			t.Errorf("maskKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// --- checkEnvFile ---

func TestCheckEnvFile(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		r := checkEnvFile("/tmp/does-not-exist-env-file")
		if r.Status != "fail" {
			t.Errorf("expected fail, got %q", r.Status)
		}
		if !strings.Contains(r.Detail, "polaris setup") {
			t.Errorf("detail should suggest setup: %s", r.Detail)
		}
	})

	t.Run("exists", func(t *testing.T) {
		f, err := os.CreateTemp("", "env-*")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Remove(f.Name()) }()
		_ = f.Close()

		r := checkEnvFile(f.Name())
		if r.Status != "ok" {
			t.Errorf("expected ok, got %q", r.Status)
		}
	})
}

// --- checkRequired ---

func TestCheckRequired(t *testing.T) {
	t.Run("all missing", func(t *testing.T) {
		vars := map[string]string{}
		results := checkRequired(vars)
		if len(results) != 4 {
			t.Fatalf("expected 4 results, got %d", len(results))
		}
		for _, r := range results {
			if r.Status != "fail" {
				t.Errorf("expected fail for %s, got %q", r.Name, r.Status)
			}
			if !strings.Contains(r.Detail, "not set") {
				t.Errorf("detail for %s should say not set: %s", r.Name, r.Detail)
			}
		}
	})

	t.Run("all set", func(t *testing.T) {
		vars := map[string]string{
			"LLM_BASE_URL": "https://api.openai.com/v1",
			"LLM_MODEL":    "gpt-4o-mini",
			"LLM_API_KEY":  "sk-test-key-12345678",
			"AUTH_TOKEN":   "pol_test_token_here",
		}
		results := checkRequired(vars)
		for _, r := range results {
			if r.Status != "ok" {
				t.Errorf("expected ok for %s, got %q", r.Name, r.Status)
			}
		}
	})

	t.Run("partial", func(t *testing.T) {
		vars := map[string]string{
			"LLM_BASE_URL": "https://api.openai.com/v1",
			// missing LLM_MODEL, LLM_API_KEY, AUTH_TOKEN
		}
		results := checkRequired(vars)
		okCount, failCount := 0, 0
		for _, r := range results {
			switch r.Status {
			case "ok":
				okCount++
			case "fail":
				failCount++
			}
		}
		if okCount != 1 {
			t.Errorf("expected 1 ok, got %d", okCount)
		}
		if failCount != 3 {
			t.Errorf("expected 3 fail, got %d", failCount)
		}
	})

	t.Run("secrets masked in display", func(t *testing.T) {
		vars := map[string]string{
			"LLM_BASE_URL": "https://api.openai.com/v1",
			"LLM_MODEL":    "gpt-4o-mini",
			"LLM_API_KEY":  "sk-long-api-key-123456",
			"AUTH_TOKEN":   "pol-long-token-123456",
		}
		results := checkRequired(vars)
		for _, r := range results {
			switch r.Name {
			case "LLM API key", "Auth token":
				if strings.Contains(r.Detail, "sk-long") || strings.Contains(r.Detail, "pol-long") {
					t.Errorf("%s detail should mask secret: %s", r.Name, r.Detail)
				}
				if !strings.Contains(r.Detail, "****") {
					t.Errorf("%s detail should contain mask: %s", r.Name, r.Detail)
				}
			}
		}
	})

	t.Run("non-secret values shown as-is", func(t *testing.T) {
		vars := map[string]string{
			"LLM_BASE_URL": "https://api.openai.com/v1",
			"LLM_MODEL":    "gpt-4o-mini",
			"LLM_API_KEY":  "x",
			"AUTH_TOKEN":   "x",
		}
		results := checkRequired(vars)
		for _, r := range results {
			if r.Name == "LLM base URL" && r.Detail != "https://api.openai.com/v1" {
				t.Errorf("LLM base URL should show full value: %s", r.Detail)
			}
			if r.Name == "LLM model" && r.Detail != "gpt-4o-mini" {
				t.Errorf("LLM model should show full value: %s", r.Detail)
			}
		}
	})
}

// --- checkVarGroup ---

func TestCheckVarGroup(t *testing.T) {
	tests := []struct {
		name   string
		vars   map[string]string
		keys   []string
		status string
		detail string
	}{
		{
			name:   "all empty",
			vars:   map[string]string{"A": "", "B": ""},
			keys:   []string{"A", "B"},
			status: "",
			detail: "",
		},
		{
			name:   "all set",
			vars:   map[string]string{"A": "1", "B": "2"},
			keys:   []string{"A", "B"},
			status: "ok",
			detail: "TEST configured",
		},
		{
			name:   "partial",
			vars:   map[string]string{"A": "1", "B": ""},
			keys:   []string{"A", "B"},
			status: "fail",
			detail: "TEST partially configured",
		},
		{
			name:   "first only",
			vars:   map[string]string{"A": "1", "B": "", "C": ""},
			keys:   []string{"A", "B", "C"},
			status: "fail",
			detail: "TEST partially configured",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, detail := checkVarGroup("TEST", tc.keys, tc.vars)
			if status != tc.status {
				t.Errorf("status = %q, want %q", status, tc.status)
			}
			if tc.detail != "" && !strings.Contains(detail, tc.detail) {
				t.Errorf("detail = %q, want to contain %q", detail, tc.detail)
			}
		})
	}
}

// --- checkGroups ---

func TestCheckGroups(t *testing.T) {
	t.Run("all empty", func(t *testing.T) {
		results := checkGroups(map[string]string{})
		if len(results) != 0 {
			t.Errorf("expected 0 results for empty groups, got %d", len(results))
		}
	})

	t.Run("partial image caption", func(t *testing.T) {
		results := checkGroups(map[string]string{
			"IMAGE_CAPTION_BASE_URL": "https://example.com",
		})
		found := false
		for _, r := range results {
			if r.Name == "Image captioner" {
				found = true
				if r.Status != "fail" {
					t.Errorf("expected fail, got %q", r.Status)
				}
			}
		}
		if !found {
			t.Error("expected Image captioner result")
		}
	})

	t.Run("full image caption", func(t *testing.T) {
		results := checkGroups(map[string]string{
			"IMAGE_CAPTION_BASE_URL": "https://example.com",
			"IMAGE_CAPTION_MODEL":    "gemini-flash",
			"IMAGE_CAPTION_API_KEY":  "key",
		})
		for _, r := range results {
			if r.Name == "Image captioner" && r.Status != "ok" {
				t.Errorf("expected ok, got %q", r.Status)
			}
		}
	})

	t.Run("partial R2", func(t *testing.T) {
		results := checkGroups(map[string]string{
			"R2_ACCOUNT_ID":    "acc",
			"R2_BUCKET":        "bkt",
			"R2_ACCESS_KEY_ID": "key",
			// missing R2_SECRET_ACCESS_KEY
		})
		found := false
		for _, r := range results {
			if r.Name == "R2 storage" {
				found = true
				if r.Status != "fail" {
					t.Errorf("expected fail for partial R2, got %q", r.Status)
				}
			}
		}
		if !found {
			t.Error("expected R2 storage result")
		}
	})

	t.Run("full R2", func(t *testing.T) {
		results := checkGroups(map[string]string{
			"R2_ACCOUNT_ID":        "acc",
			"R2_BUCKET":            "bkt",
			"R2_ACCESS_KEY_ID":     "key",
			"R2_SECRET_ACCESS_KEY": "secret",
		})
		for _, r := range results {
			if r.Name == "R2 storage" && r.Status != "ok" {
				t.Errorf("expected ok for full R2, got %q", r.Status)
			}
		}
	})
}

// --- checkAuthToken ---

func TestCheckAuthToken(t *testing.T) {
	tests := []struct {
		name   string
		token  string
		status string
	}{
		{name: "missing", status: "fail"},
		{name: "default", token: "change-me", status: "fail"},
		{name: "short", token: "short", status: "warn"},
		{name: "good", token: "pol_this_is_a_good_token_with_enough_entropy", status: "ok"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{"AUTH_TOKEN": tc.token}
			r := checkAuthToken(vars)
			if r.Status != tc.status {
				t.Errorf("status = %q, want %q", r.Status, tc.status)
			}
		})
	}
}

// --- checkLLMConnectivity with httptest ---

func TestCheckLLMConnectivity_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer valid-key" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()

	vars := map[string]string{
		"LLM_BASE_URL": server.URL,
		"LLM_MODEL":    "test-model",
		"LLM_API_KEY":  "valid-key",
	}
	r := checkLLMConnectivity(context.Background(), vars, false)
	if r.Status != "ok" {
		t.Errorf("status = %q, detail = %q", r.Status, r.Detail)
	}
}

func TestCheckLLMConnectivity_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer server.Close()

	vars := map[string]string{
		"LLM_BASE_URL": server.URL,
		"LLM_MODEL":    "test-model",
		"LLM_API_KEY":  "bad-key",
	}
	r := checkLLMConnectivity(context.Background(), vars, false)
	if r.Status != "fail" {
		t.Errorf("expected fail for 401, got %q", r.Status)
	}
	if !strings.Contains(r.Detail, "401") {
		t.Errorf("detail should mention 401: %s", r.Detail)
	}
}

func TestCheckLLMConnectivity_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	vars := map[string]string{
		"LLM_BASE_URL": server.URL,
		"LLM_MODEL":    "test-model",
		"LLM_API_KEY":  "some-key",
	}
	r := checkLLMConnectivity(context.Background(), vars, false)
	if r.Status != "warn" {
		t.Errorf("expected warn for 500, got %q", r.Status)
	}
}

func TestCheckLLMConnectivity_ConnectionRefused(t *testing.T) {
	// Use a port that's not listening
	vars := map[string]string{
		"LLM_BASE_URL": "http://127.0.0.1:1",
		"LLM_MODEL":    "test",
		"LLM_API_KEY":  "key",
	}
	r := checkLLMConnectivity(context.Background(), vars, false)
	if r.Status != "fail" && r.Status != "warn" {
		t.Errorf("expected fail or warn for connection refused, got %q", r.Status)
	}
}

func TestCheckLLMConnectivity_MissingVars(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
	}{
		{"all empty", map[string]string{}},
		{"missing api key", map[string]string{"LLM_BASE_URL": "https://api.openai.com/v1", "LLM_MODEL": "gpt-4"}},
		{"missing model", map[string]string{"LLM_BASE_URL": "https://api.openai.com/v1", "LLM_API_KEY": "key"}},
		{"missing url", map[string]string{"LLM_MODEL": "gpt-4", "LLM_API_KEY": "key"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := checkLLMConnectivity(context.Background(), tc.vars, false)
			if r.Status != "fail" {
				t.Errorf("expected fail, got %q", r.Status)
			}
		})
	}
}

func TestCheckLLMConnectivity_TrailingSlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	defer server.Close()

	vars := map[string]string{
		"LLM_BASE_URL": server.URL + "/", // trailing slash
		"LLM_MODEL":    "test",
		"LLM_API_KEY":  "key",
	}
	r := checkLLMConnectivity(context.Background(), vars, false)
	if r.Status != "ok" {
		t.Errorf("status = %q, detail = %q", r.Status, r.Detail)
	}
}

// --- checkDocker ---

func TestCheckDocker_NotFound(t *testing.T) {
	orig := execLookPath
	execLookPath = func(name string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() { execLookPath = orig }()

	r := checkDocker()
	if r.Status != "fail" {
		t.Errorf("expected fail when docker not found, got %q", r.Status)
	}
	if !strings.Contains(r.Detail, "requires Docker") {
		t.Errorf("detail should mention Docker requirement: %s", r.Detail)
	}
}

func TestCheckDocker_DaemonDown(t *testing.T) {
	orig := execLookPath
	execLookPath = func(name string) (string, error) {
		return "/usr/bin/docker", nil
	}
	defer func() { execLookPath = orig }()

	origPing := dockerPing
	dockerPing = func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("connection refused")
	}
	defer func() { dockerPing = origPing }()

	r := checkDocker()
	if r.Status != "fail" {
		t.Errorf("expected fail when daemon down, got %q", r.Status)
	}
	if !strings.Contains(r.Detail, "daemon not responding") {
		t.Errorf("detail should mention daemon: %s", r.Detail)
	}
}

func TestCheckDocker_Running(t *testing.T) {
	orig := execLookPath
	execLookPath = func(name string) (string, error) {
		return "/usr/bin/docker", nil
	}
	defer func() { execLookPath = orig }()

	origPing := dockerPing
	dockerPing = func(ctx context.Context) (string, error) { return "24.0.0", nil }
	defer func() { dockerPing = origPing }()

	r := checkDocker()
	if r.Status != "ok" {
		t.Errorf("expected ok, got %q", r.Status)
	}
	if !strings.Contains(r.Detail, "24.0.0") {
		t.Errorf("detail should contain version: %s", r.Detail)
	}
}

// --- isConnectionRefused ---

func TestIsConnectionRefused(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if isConnectionRefused(nil) {
			t.Error("nil should not be connection refused")
		}
	})

	t.Run("op error connection refused", func(t *testing.T) {
		err := &net.OpError{Op: "dial", Err: fmt.Errorf("connection refused")}
		if !isConnectionRefused(err) {
			t.Error("OpError should be connection refused")
		}
	})

	t.Run("dns error", func(t *testing.T) {
		err := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host"}}
		if isConnectionRefused(err) {
			t.Error("DNS error should not be connection refused")
		}
	})

	t.Run("generic error", func(t *testing.T) {
		if isConnectionRefused(fmt.Errorf("something else")) {
			t.Error("generic error should not be connection refused")
		}
	})
}

// --- PrintResults ---

func TestPrintResults_AllOk(t *testing.T) {
	results := []CheckResult{
		{Name: "check1", Status: "ok", Detail: "good"},
		{Name: "check2", Status: "ok", Detail: "good"},
	}
	var buf strings.Builder
	PrintResults(&buf, results)
	output := buf.String()
	if !strings.Contains(output, "2 passed") {
		t.Errorf("should say 2 passed: %s", output)
	}
	if !strings.Contains(output, "All checks passed") {
		t.Errorf("should say all checks passed: %s", output)
	}
}

func TestPrintResults_WithWarnings(t *testing.T) {
	results := []CheckResult{
		{Name: "check1", Status: "ok", Detail: "good"},
		{Name: "check2", Status: "warn", Detail: "watch out"},
	}
	var buf strings.Builder
	PrintResults(&buf, results)
	output := buf.String()
	if !strings.Contains(output, "1 warnings") {
		t.Errorf("should say 1 warning: %s", output)
	}
	if !strings.Contains(output, "consider addressing warnings") {
		t.Errorf("should mention warnings: %s", output)
	}
}

func TestPrintResults_WithFailures(t *testing.T) {
	results := []CheckResult{
		{Name: "check1", Status: "fail", Detail: "broken"},
	}
	var buf strings.Builder
	PrintResults(&buf, results)
	output := buf.String()
	if !strings.Contains(output, "1 failures") {
		t.Errorf("should say 1 failure: %s", output)
	}
	if !strings.Contains(output, "Fix errors above") {
		t.Errorf("should mention fixing: %s", output)
	}
}

func TestPrintResults_Icons(t *testing.T) {
	results := []CheckResult{
		{Name: "ok-check", Status: "ok", Detail: ""},
		{Name: "warn-check", Status: "warn", Detail: ""},
		{Name: "fail-check", Status: "fail", Detail: ""},
	}
	var buf strings.Builder
	PrintResults(&buf, results)
	output := buf.String()
	if !strings.Contains(output, "✓") {
		t.Error("should contain checkmark for ok")
	}
	if !strings.Contains(output, "⚠") {
		t.Error("should contain warning icon")
	}
	if !strings.Contains(output, "✗") {
		t.Error("should contain cross for fail")
	}
}

func TestPrintResults_Empty(t *testing.T) {
	var buf strings.Builder
	PrintResults(&buf, nil)
	output := buf.String()
	if !strings.Contains(output, "0 passed") {
		t.Errorf("should handle empty results: %s", output)
	}
}

// --- Run integration ---

func TestRun_Integration_AllGood(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	content := `LLM_BASE_URL=https://api.openai.com/v1
LLM_MODEL=gpt-4o-mini
LLM_API_KEY=sk-test-key
AUTH_TOKEN=pol_a_very_long_token_for_testing_purposes
`
	_ = os.WriteFile(envPath, []byte(content), 0o600)

	// Mock Docker check
	orig := execLookPath
	execLookPath = func(name string) (string, error) { return "/usr/bin/docker", nil }
	defer func() { execLookPath = orig }()

	origPing := dockerPing
	dockerPing = func(ctx context.Context) (string, error) { return "24.0.0", nil }
	defer func() { dockerPing = origPing }()

	results := Run(context.Background(), envPath, false)

	// Everything should pass except LLM connectivity (no real server)
	for _, r := range results {
		if r.Name == "LLM connectivity" {
			continue
		}
		if r.Status == "fail" {
			t.Errorf("unexpected failure: %s — %s", r.Name, r.Detail)
		}
	}
}

func TestRun_Integration_NoEnvFile(t *testing.T) {
	dir := t.TempDir()

	// Mock Docker check
	orig := execLookPath
	execLookPath = func(name string) (string, error) { return "/usr/bin/docker", nil }
	defer func() { execLookPath = orig }()

	origPing := dockerPing
	dockerPing = func(ctx context.Context) (string, error) { return "24.0.0", nil }
	defer func() { dockerPing = origPing }()

	results := Run(context.Background(), filepath.Join(dir, "missing.env"), false)

	// Should have failures for env file and all required vars
	failCount := 0
	for _, r := range results {
		if r.Status == "fail" {
			failCount++
		}
	}
	if failCount == 0 {
		t.Error("expected some failures for missing env file")
	}
}

func TestRun_Integration_PartialConfig(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")

	// Only some required vars, plus partial IMAGE_CAPTION
	content := `LLM_BASE_URL=https://api.openai.com/v1
IMAGE_CAPTION_BASE_URL=https://example.com
AUTH_TOKEN=change-me
`
	_ = os.WriteFile(envPath, []byte(content), 0o600)

	orig := execLookPath
	execLookPath = func(name string) (string, error) { return "/usr/bin/docker", nil }
	defer func() { execLookPath = orig }()

	origPing := dockerPing
	dockerPing = func(ctx context.Context) (string, error) { return "24.0.0", nil }
	defer func() { dockerPing = origPing }()

	results := Run(context.Background(), envPath, false)

	// Should detect:
	// - LLM_MODEL missing (fail)
	// - LLM_API_KEY missing (fail)
	// - AUTH_TOKEN = change-me (fail)
	// - IMAGE_CAPTION_* partial (fail)
	failNames := map[string]bool{}
	for _, r := range results {
		if r.Status == "fail" && r.Name != "LLM connectivity" {
			failNames[r.Name] = true
		}
	}
	for _, expected := range []string{"LLM model", "LLM API key", "Auth token", "Image captioner"} {
		if !failNames[expected] {
			t.Errorf("expected failure for %q", expected)
		}
	}
}
