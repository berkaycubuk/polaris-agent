// Package doctor runs diagnostic checks on a Polaris Agent configuration
// and reports issues with actionable fix instructions.
package doctor

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CheckResult is the outcome of a single diagnostic check.
type CheckResult struct {
	Name   string // short check name
	Status string // "ok", "warn", "fail"
	Detail string // human-readable explanation
}

// Run executes all diagnostic checks against the given env file.
// It reads .env directly (does not use config.Load to avoid failing on
// missing vars — doctor reports what's missing).
func Run(ctx context.Context, envPath string, verbose bool) []CheckResult {
	var results []CheckResult

	vars := parseEnvFile(envPath)

	// 1. Config file exists
	results = append(results, checkEnvFile(envPath))

	// 2. Required env vars
	results = append(results, checkRequired(vars)...)

	// 3. Variable group consistency
	results = append(results, checkGroups(vars)...)

	// 4. LLM connectivity
	results = append(results, checkLLMConnectivity(ctx, vars, verbose))

	// 5. Auth token strength
	results = append(results, checkAuthToken(vars))

	// 6. Docker
	results = append(results, checkDocker())

	return results
}

// PrintResults formats and writes check results to w.
func PrintResults(w io.Writer, results []CheckResult) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Polaris Agent — Diagnostics")
	fmt.Fprintln(w, "  ─────────────────────────────────")
	fmt.Fprintln(w)

	for _, r := range results {
		icon := "✓"
		switch r.Status {
		case "warn":
			icon = "⚠"
		case "fail":
			icon = "✗"
		}
		fmt.Fprintf(w, "  %s %-30s %s\n", icon, r.Name, r.Detail)
	}

	// Summary
	ok, warns, fails := 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case "ok":
			ok++
		case "warn":
			warns++
		case "fail":
			fails++
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %d passed, %d warnings, %d failures\n", ok, warns, fails)

	if fails > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Fix errors above, then re-run: polaris doctor")
	} else if warns > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  Agent should work, but consider addressing warnings above.")
	} else {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  All checks passed! Start your agent: docker compose up -d")
	}
	fmt.Fprintln(w)
}

// --- Individual checks ---

func checkEnvFile(path string) CheckResult {
	if _, err := os.Stat(path); err != nil {
		return CheckResult{
			Name:   ".env file",
			Status: "fail",
			Detail: fmt.Sprintf("not found at %s — run: polaris setup", path),
		}
	}
	return CheckResult{Name: ".env file", Status: "ok", Detail: path}
}

func checkRequired(vars map[string]string) []CheckResult {
	required := []struct {
		key, label string
	}{
		{"LLM_BASE_URL", "LLM base URL"},
		{"LLM_MODEL", "LLM model"},
		{"LLM_API_KEY", "LLM API key"},
		{"AUTH_TOKEN", "Auth token"},
	}

	var results []CheckResult
	for _, r := range required {
		if v := vars[r.key]; v == "" {
			results = append(results, CheckResult{
				Name:   r.label,
				Status: "fail",
				Detail: fmt.Sprintf("not set — add %s to .env", r.key),
			})
		} else {
			display := v
			if r.key == "LLM_API_KEY" || r.key == "AUTH_TOKEN" {
				display = maskKey(v)
			}
			results = append(results, CheckResult{
				Name:   r.label,
				Status: "ok",
				Detail: display,
			})
		}
	}
	return results
}

func checkGroups(vars map[string]string) []CheckResult {
	var results []CheckResult

	// IMAGE_CAPTION_* group
	capVars := []string{"IMAGE_CAPTION_BASE_URL", "IMAGE_CAPTION_MODEL", "IMAGE_CAPTION_API_KEY"}
	if status, detail := checkVarGroup("IMAGE_CAPTION_*", capVars, vars); status != "" {
		results = append(results, CheckResult{Name: "Image captioner", Status: status, Detail: detail})
	}

	// R2_* group
	r2Vars := []string{"R2_ACCOUNT_ID", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"}
	if status, detail := checkVarGroup("R2_*", r2Vars, vars); status != "" {
		results = append(results, CheckResult{Name: "R2 storage", Status: status, Detail: detail})
	}

	return results
}

func checkVarGroup(label string, keys []string, vars map[string]string) (status, detail string) {
	anySet, allSet := false, true
	for _, k := range keys {
		if vars[k] != "" {
			anySet = true
		} else {
			allSet = false
		}
	}
	if anySet && !allSet {
		return "fail", fmt.Sprintf("%s partially configured — set all or none", label)
	}
	if allSet {
		return "ok", fmt.Sprintf("%s configured", label)
	}
	return "", "" // not configured at all — skip (optional)
}

func checkLLMConnectivity(ctx context.Context, vars map[string]string, verbose bool) CheckResult {
	baseURL := vars["LLM_BASE_URL"]
	apiKey := vars["LLM_API_KEY"]
	model := vars["LLM_MODEL"]

	if baseURL == "" || apiKey == "" || model == "" {
		return CheckResult{
			Name:   "LLM connectivity",
			Status: "fail",
			Detail: "skipped — missing LLM_BASE_URL, LLM_API_KEY, or LLM_MODEL",
		}
	}

	url := strings.TrimRight(baseURL, "/") + "/models"

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return CheckResult{
			Name:   "LLM connectivity",
			Status: "fail",
			Detail: fmt.Sprintf("invalid URL: %v", err),
		}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if isConnectionRefused(err) {
			return CheckResult{
				Name:   "LLM connectivity",
				Status: "fail",
				Detail: fmt.Sprintf("cannot reach %s — is the endpoint running?", baseURL),
			}
		}
		return CheckResult{
			Name:   "LLM connectivity",
			Status: "warn",
			Detail: fmt.Sprintf("network error: %v", err),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return CheckResult{
			Name:   "LLM connectivity",
			Status: "fail",
			Detail: "API key rejected (401) — check LLM_API_KEY",
		}
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return CheckResult{
			Name:   "LLM connectivity",
			Status: "warn",
			Detail: fmt.Sprintf("HTTP %d from %s: %s", resp.StatusCode, baseURL, strings.TrimSpace(string(body))),
		}
	}

	return CheckResult{
		Name:   "LLM connectivity",
		Status: "ok",
		Detail: fmt.Sprintf("connected to %s", baseURL),
	}
}

func checkAuthToken(vars map[string]string) CheckResult {
	token := vars["AUTH_TOKEN"]
	if token == "" {
		return CheckResult{
			Name:   "Auth token",
			Status: "fail",
			Detail: "not set",
		}
	}
	if token == "change-me" {
		return CheckResult{
			Name:   "Auth token",
			Status: "fail",
			Detail: "still using default \"change-me\" — run: polaris setup",
		}
	}
	if len(token) < 16 {
		return CheckResult{
			Name:   "Auth token strength",
			Status: "warn",
			Detail: fmt.Sprintf("token is short (%d chars) — consider running: polaris setup", len(token)),
		}
	}
	return CheckResult{
		Name:   "Auth token",
		Status: "ok",
		Detail: fmt.Sprintf("set (%d chars)", len(token)),
	}
}

func checkDocker() CheckResult {
	// Check if docker is available
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return CheckResult{
			Name:   "Docker",
			Status: "ok",
			Detail: "running inside container",
		}
	}

	path, err := execLookPath("docker")
	if err != nil {
		return CheckResult{
			Name:   "Docker",
			Status: "fail",
			Detail: "not found — Polaris requires Docker. Install: https://docs.docker.com/get-docker/",
		}
	}

	// Try to reach the Docker daemon via `docker info`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	version, err := dockerPing(ctx)
	if err != nil {
		return CheckResult{
			Name:   "Docker",
			Status: "fail",
			Detail: fmt.Sprintf("found at %s but daemon not responding — start Docker", path),
		}
	}

	return CheckResult{
		Name:   "Docker",
		Status: "ok",
		Detail: fmt.Sprintf("running (server %s)", version),
	}
}

// --- Helpers ---

func parseEnvFile(path string) map[string]string {
	vars := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return vars
	}
	defer f.Close()
	return parseEnvFileFromReader(f)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

func isConnectionRefused(err error) bool {
	if opErr, ok := err.(*net.OpError); ok {
		if _, ok := opErr.Err.(*net.DNSError); ok {
			return false
		}
		return true
	}
	return false
}

// execLookPath is a variable for testability.
var execLookPath = exec.LookPath

// dockerPing attempts to reach the Docker daemon via `docker info`.
// Returns the server version string on success.
// Overridden in tests to avoid subprocess calls.
var dockerPing = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parseEnvFileFromReader is exported for testing.
func parseEnvFileFromReader(r io.Reader) map[string]string {
	vars := map[string]string{}
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		vars[k] = v
	}
	return vars
}
