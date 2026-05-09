package scheduler

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ScriptRunner executes a script-kind job and returns whatever stdout the
// script produced. Stderr is captured separately and folded into the error
// message on non-zero exit so the deliverer doesn't push debug noise to
// Telegram. The scheduler treats the returned string the same way it treats
// an LLM reply: empty → no Telegram push, non-empty → message body.
type ScriptRunner interface {
	RunScript(ctx context.Context, job Job) (string, error)
}

// ExecScriptRunner runs scripts with `uv run <path>` from the data directory.
// Subprocesses inherit a curated env (the same one the bash tool uses) so
// SKILL_* secrets and SKILL_PUBLIC_* identifiers are available to scripts.
type ExecScriptRunner struct {
	DataDir string
	Env     []string      // child env (typically tools.Registry.ChildEnv())
	Timeout time.Duration // per-fire wall clock cap (0 = 5m)
}

// NewExecScriptRunner builds a script runner. env may be nil — in that case
// the subprocess inherits os.Environ(), which is fine for tests but leaks
// secrets in production; pass the redacted child env in real wiring.
func NewExecScriptRunner(dataDir string, env []string, timeout time.Duration) *ExecScriptRunner {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &ExecScriptRunner{DataDir: dataDir, Env: env, Timeout: timeout}
}

func (r *ExecScriptRunner) RunScript(ctx context.Context, job Job) (string, error) {
	if job.Script == "" {
		return "", fmt.Errorf("job %s has empty Script path", job.ID)
	}
	abs, err := resolveInside(r.DataDir, job.Script)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("script %s: %w", job.Script, err)
	}

	cctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	// `uv run` handles PEP 723 inline metadata, ad-hoc scripts, and
	// pyproject.toml-backed scripts — covers all script layouts in one shot.
	cmd := exec.CommandContext(cctx, "uv", "run", abs)
	cmd.Dir = r.DataDir
	if len(r.Env) > 0 {
		cmd.Env = r.Env
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\n")
	if runErr != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = runErr.Error()
		}
		return out, fmt.Errorf("script exited with error: %s", errMsg)
	}
	return out, nil
}

// resolveInside is the scheduler-package equivalent of tools.resolvePath.
// Kept local to avoid an import cycle (tools imports scheduler, not the
// other way round).
func resolveInside(dataDir, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(dataDir, p)
	}
	abs = filepath.Clean(abs)
	rootAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) && abs != rootAbs {
		return "", fmt.Errorf("path escapes data dir: %s", p)
	}
	return abs, nil
}
