package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// safeguard inspects a bash command and returns an error if it appears
// destructive. This is heuristic, not a sandbox: a determined caller can
// bypass it via base64 strings, `bash -c`, or `python -c`. The goal is to
// catch the realistic failure mode — a buggy/runaway LLM emitting an
// obviously catastrophic command — and to force structured tools
// (write_file, manage_skill) to be used for protected files.
func safeguard(command string) error {
	cmd := strings.TrimSpace(command)
	if reason, ok := matchDangerous(cmd); ok {
		return fmt.Errorf("blocked by bash safeguard: %s. Narrow the scope (specify exact files) and retry", reason)
	}
	for _, f := range protectedFiles {
		if mutatesPath(cmd, f) {
			return fmt.Errorf("blocked: bash cannot mutate %s — use write_file (or read_file to inspect)", f)
		}
	}
	for _, d := range protectedDirs {
		if mutatesPath(cmd, d) {
			return fmt.Errorf("blocked: bash cannot mutate %s/ — use manage_skill (create/edit/archive). Reading via bash (cat, ls, grep) is fine", d)
		}
	}
	return nil
}

// protectedFiles must be modified through write_file, never bash. The
// system prompt and tooling depend on these living at known paths.
var protectedFiles = []string{
	"SOUL.md",
	"USER.md",
	"MEMORY.md",
}

// protectedDirs must be modified through manage_skill, never bash. .archive
// is locked so the agent can't undo its own safety net.
var protectedDirs = []string{
	"skills",
	"skills/.archive",
}

// rmFlags matches an rm flag set that combines -r and -f in any order
// (-rf, -fr, -Rf, -fR, --recursive --force, etc.).
var rmFlags = `(?:-[a-zA-Z]*r[a-zA-Z]*f[a-zA-Z]*|-[a-zA-Z]*f[a-zA-Z]*r[a-zA-Z]*|-r\s+-f|-f\s+-r|--recursive\s+--force|--force\s+--recursive)`

var (
	// rm -rf <wide-target> — blocks the most common catastrophic shape.
	rmRfRe = regexp.MustCompile(`(?:^|[^a-zA-Z0-9_-])rm\s+` + rmFlags + `\s+(\S+)`)

	// find ... -delete or find ... -exec rm
	findDeleteRe  = regexp.MustCompile(`\bfind\s+\S.*-delete\b`)
	findExecRmRe  = regexp.MustCompile(`\bfind\s+\S.*-exec\s+rm\b`)
	findExecRmDir = regexp.MustCompile(`\bfind\s+\S.*-execdir\s+rm\b`)

	// disk / filesystem destruction
	ddOfDevRe   = regexp.MustCompile(`\bdd\s+[^|;]*of=/dev/`)
	mkfsRe      = regexp.MustCompile(`\bmkfs\.[a-z0-9]+\b`)
	devRedirect = regexp.MustCompile(`>\s*/dev/sd[a-z]`)

	// fork bomb
	forkBombRe = regexp.MustCompile(`:\s*\(\s*\)\s*\{\s*:\s*\|\s*:?\s*&\s*\}\s*;\s*:`)

	// chmod -R 000 / and friends
	chmodWideRe = regexp.MustCompile(`\bchmod\s+(?:-[a-zA-Z]*R[a-zA-Z]*|--recursive)\s+\d+\s+/(\s|$)`)
)

// wideTargets are filesystem locations that should never be the target of
// rm -rf. They reduce to "everything that matters" — the root, the cwd
// (which is the data dir for our bash tool), the parent, the home dir, and
// the unquoted glob.
var wideTargets = map[string]bool{
	"/":      true,
	"/*":     true,
	".":      true,
	"./":     true,
	"./*":    true,
	"..":     true,
	"../":    true,
	"../*":   true,
	"~":      true,
	"~/":     true,
	"~/*":    true,
	"$HOME":  true,
	"$HOME/": true,
	"*":      true,
}

func matchDangerous(cmd string) (string, bool) {
	if m := rmRfRe.FindStringSubmatch(cmd); m != nil {
		target := strings.Trim(m[1], `"'`)
		if wideTargets[target] {
			return "rm -rf with a filesystem-wide target (" + target + ")", true
		}
	}
	if findDeleteRe.MatchString(cmd) {
		return "find -delete is unbounded — list files explicitly", true
	}
	if findExecRmRe.MatchString(cmd) || findExecRmDir.MatchString(cmd) {
		return "find -exec rm is unbounded", true
	}
	if ddOfDevRe.MatchString(cmd) {
		return "dd writing to /dev/*", true
	}
	if mkfsRe.MatchString(cmd) {
		return "mkfs reformats devices", true
	}
	if devRedirect.MatchString(cmd) {
		return "redirect to /dev/sd*", true
	}
	if forkBombRe.MatchString(cmd) {
		return "fork bomb", true
	}
	if chmodWideRe.MatchString(cmd) {
		return "chmod -R on /", true
	}
	return "", false
}

// mutatesPath returns true if cmd appears to write to or delete `path`. It
// matches common write shapes: redirects (> >>), rm/mv/cp/tee/sed -i with
// `path` (or `./path`) as a destination, and rm of a directory. False
// positives (e.g. `grep -v skills/old.md`) are tolerated — the agent can
// retry through the structured tool.
func mutatesPath(cmd, path string) bool {
	q := regexp.QuoteMeta(path)
	prefix := `(?:\./)?` + q + `(?:/|\s|$|"|')`

	patterns := []string{
		`>>?\s*` + prefix,                                // > path, >> path
		`\brm\s+(?:-[a-zA-Z]+\s+)*` + prefix,             // rm [-flags] path
		`\bmv\s+(?:-[a-zA-Z]+\s+)*\S+\s+` + prefix,       // mv X path
		`\bcp\s+(?:-[a-zA-Z]+\s+)*\S+\s+` + prefix,       // cp X path
		`\btee\s+(?:-[a-zA-Z]+\s+)*` + prefix,            // tee path
		`\bsed\s+(?:-[a-zA-Z]+\s+)*-i\s+\S+\s+` + prefix, // sed -i X path
		`\btruncate\s+(?:-[a-zA-Z]+\s+)*\S+\s+` + prefix, // truncate -s X path
		`\bln\s+(?:-[a-zA-Z]+\s+)*\S+\s+` + prefix,       // ln [-s] X path
	}
	for _, p := range patterns {
		if regexp.MustCompile(p).MatchString(cmd) {
			return true
		}
	}
	return false
}
