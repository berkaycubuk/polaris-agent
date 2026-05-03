package tools

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// secretRedactor decides which environment variables can be inherited by
// child processes (the bash tool's subprocesses) and redacts any known
// secret values from tool output before it returns to the LLM.
//
// Two channels are recognised as user-supplied skill secrets:
//   - environment variables prefixed with SKILL_ (typically loaded from .env)
//   - any file under <dataDir>/secrets/ (provisioned by the user out-of-band)
//
// In addition, parent-process variables whose *names* match common secret
// substrings (KEY, TOKEN, SECRET, PASSWORD, ...) have their *values*
// registered for redaction even though they are NOT exported to children.
// That covers Polaris's own config (LLM_API_KEY, AUTH_TOKEN, etc.) so the
// agent can't echo it back in tool output.
type secretRedactor struct {
	childEnv     []string
	values       []string
	skillNames   []string // names of SKILL_* (secret) vars, for the system prompt
	publicNames  []string // names of SKILL_PUBLIC_* (non-secret) vars
	secretsFiles []string // basenames of files under secrets/, for the system prompt
}

const (
	skillEnvPrefix       = "SKILL_"
	publicEnvPrefix      = "SKILL_PUBLIC_"
	minRedactValueLength = 12
)

// safeEnvPrefixes are baseline variables that subprocesses can keep —
// nothing here should be sensitive on its own.
var safeEnvPrefixes = []string{
	"PATH", "HOME", "USER", "LOGNAME", "PWD", "SHELL",
	"LANG", "LC_", "TERM",
	"TMPDIR", "TMP", "TEMP",
	"PYTHONPATH", "VIRTUAL_ENV", "UV_",
}

// secretSubstrings flag a var name as secret-looking even without a SKILL_ prefix.
var secretSubstrings = []string{
	"KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "AUTH",
}

// newSecretRedactor builds the child env and the redaction list.
func newSecretRedactor(secretsDir string) *secretRedactor {
	r := &secretRedactor{}
	seen := map[string]struct{}{}

	addValue := func(v string) {
		v = strings.TrimSpace(v)
		if len(v) < minRedactValueLength {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		r.values = append(r.values, v)
	}

	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := kv[:eq]
		v := kv[eq+1:]

		// SKILL_PUBLIC_* vars: export to children, do NOT redact.
		// For OAuth client_ids, public webhook IDs, anything the user-facing
		// flow legitimately needs to surface (e.g. into an auth URL).
		// Must check BEFORE the SKILL_ branch since SKILL_PUBLIC_ has the
		// same SKILL_ prefix.
		if strings.HasPrefix(k, publicEnvPrefix) {
			r.childEnv = append(r.childEnv, kv)
			r.publicNames = append(r.publicNames, k)
			continue
		}

		// SKILL_* vars: redact AND export to children.
		if strings.HasPrefix(k, skillEnvPrefix) {
			addValue(v)
			r.childEnv = append(r.childEnv, kv)
			r.skillNames = append(r.skillNames, k)
			continue
		}

		// Names matching secret substrings: redact only (do NOT export).
		upper := strings.ToUpper(k)
		isSecret := false
		for _, s := range secretSubstrings {
			if strings.Contains(upper, s) {
				isSecret = true
				break
			}
		}
		if isSecret {
			addValue(v)
			continue
		}

		// Allow-listed system vars: export, no redaction.
		for _, p := range safeEnvPrefixes {
			if strings.HasPrefix(k, p) {
				r.childEnv = append(r.childEnv, kv)
				break
			}
		}
	}

	if secretsDir != "" {
		_ = filepath.WalkDir(secretsDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			rel, relErr := filepath.Rel(secretsDir, path)
			if relErr != nil {
				rel = filepath.Base(path)
			}
			r.secretsFiles = append(r.secretsFiles, rel)

			content := strings.TrimSpace(string(data))
			addValue(content)
			// .env-style files: also register each value separately so a
			// single token from a multi-line file gets redacted on its own.
			for _, line := range strings.Split(content, "\n") {
				if eq := strings.IndexByte(line, '='); eq >= 0 {
					val := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
					addValue(val)
				}
			}
			return nil
		})
	}

	// Longest first so a long secret isn't partially clobbered by a shorter
	// substring of itself.
	sort.Slice(r.values, func(i, j int) bool {
		return len(r.values[i]) > len(r.values[j])
	})
	return r
}

// Redact replaces every known secret value in s with "[REDACTED]".
func (r *secretRedactor) Redact(s string) string {
	if r == nil || len(r.values) == 0 {
		return s
	}
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, "[REDACTED]")
	}
	return s
}

// ChildEnv returns the env slice to pass to subprocesses.
func (r *secretRedactor) ChildEnv() []string {
	if r == nil {
		return os.Environ()
	}
	out := make([]string, len(r.childEnv))
	copy(out, r.childEnv)
	return out
}

// SkillEnvNames returns the names (not values) of secret SKILL_* env
// vars detected at startup, sorted. Excludes SKILL_PUBLIC_* (those are
// returned by PublicEnvNames). Used to show the agent which secrets are
// available without leaking the values into the system prompt.
func (r *secretRedactor) SkillEnvNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.skillNames))
	copy(out, r.skillNames)
	sort.Strings(out)
	return out
}

// PublicEnvNames returns the names of SKILL_PUBLIC_* env vars — passed
// to subprocesses and NOT redacted from tool output. For non-secret
// identifiers (OAuth client_id, public webhook IDs, etc.) that scripts
// legitimately need to surface to the user.
func (r *secretRedactor) PublicEnvNames() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.publicNames))
	copy(out, r.publicNames)
	sort.Strings(out)
	return out
}

// SecretsFiles returns relative paths of files found under secrets/ at
// startup, sorted. Names only, not contents.
func (r *secretRedactor) SecretsFiles() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.secretsFiles))
	copy(out, r.secretsFiles)
	sort.Strings(out)
	return out
}
