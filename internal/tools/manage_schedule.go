package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
	"github.com/berkaycubuk/polaris-agent/internal/scheduler"
	"github.com/berkaycubuk/polaris-agent/internal/session"
)

// scheduleFirer is the subset of *scheduler.Scheduler the tool calls. The
// indirection keeps the tool testable without spinning up a real scheduler.
type scheduleFirer interface {
	FireNow(ctx context.Context, jobID string) error
	SetNextRun(jobID string) error
}

// manageSchedule is the agent's interface to the cron scheduler. Single
// compressed action-oriented tool, mirroring manage_skill / manage_memory.
type manageSchedule struct {
	store   *scheduler.Store
	sched   scheduleFirer
	dataDir string // root for script files (set by registry; tests can leave empty)
}

func (t *manageSchedule) Name() string { return "manage_schedule" }

func (t *manageSchedule) Spec() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunc{
			Name: "manage_schedule",
			Description: "Schedule background jobs that fire later and deliver their reply back to the current chat. " +
				"Use this when the user asks for a reminder, a recurring check-in, or a future task — anything that " +
				"should run without them prompting again.\n\n" +
				"Two kinds of jobs:\n" +
				"- kind=\"agent\" (default): runs an LLM turn with the given prompt. Use when the work needs reasoning, " +
				"web/wiki lookups, or tool calls.\n" +
				"- kind=\"script\": runs a Python script (via `uv run`) and pipes stdout into the chat. Use for " +
				"deterministic checks (calendar fetch, scrape, ping) where invoking an LLM is overkill or error-prone. " +
				"Stderr is hidden so debug prints (file=sys.stderr) don't reach the user. Empty stdout = no message. " +
				"PEP 723 inline deps work; secrets in SKILL_*/secrets/ are available.\n\n" +
				"Actions:\n" +
				"- create: schedule a new job. Agent jobs need prompt; script jobs need script (the .py source).\n" +
				"- list: show all jobs (id, name, kind, schedule, state, next run)\n" +
				"- remove: delete a job by id (script files are deleted too)\n" +
				"- pause / resume: toggle whether a job fires\n" +
				"- run: fire a job immediately (for testing)\n\n" +
				"Schedule formats: \"30m\", \"2h\", \"1d\" (one-shot from now); \"every 30m\", \"every 2h\" (recurring); " +
				"or an RFC3339 timestamp like \"2026-02-03T14:00:00Z\". Cron expressions are not supported.\n\n" +
				"Agent jobs run in a fresh session with no chat history — the prompt MUST be self-contained. " +
				"Cron-originated runs cannot create more jobs (no recursion).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"create", "list", "remove", "pause", "resume", "run"},
						"description": "The action to perform.",
					},
					"job_id": map[string]any{
						"type":        "string",
						"description": "Required for remove/pause/resume/run.",
					},
					"kind": map[string]any{
						"type":        "string",
						"enum":        []string{"agent", "script"},
						"description": "Job kind for create. Defaults to \"agent\".",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "Required for create when kind=\"agent\". The full self-contained prompt the future agent run will execute.",
					},
					"script": map[string]any{
						"type":        "string",
						"description": "Required for create when kind=\"script\". The full Python source. The tool writes it to schedule/scripts/<job_id>.py and runs it via `uv run`. Whatever the script prints to stdout becomes the chat message.",
					},
					"schedule": map[string]any{
						"type":        "string",
						"description": "Required for create. e.g. \"30m\", \"every 2h\", \"2026-02-03T14:00:00Z\".",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Optional human-friendly name for create.",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

type scheduleArgs struct {
	Action   string `json:"action"`
	JobID    string `json:"job_id"`
	Kind     string `json:"kind"`
	Prompt   string `json:"prompt"`
	Script   string `json:"script"`
	Schedule string `json:"schedule"`
	Name     string `json:"name"`
}

func (t *manageSchedule) Run(ctx context.Context, args string) (string, error) {
	var a scheduleArgs
	if err := json.Unmarshal([]byte(args), &a); err != nil {
		return "", err
	}
	switch a.Action {
	case "create":
		return t.create(ctx, a)
	case "list":
		return t.list()
	case "remove":
		return t.remove(a.JobID)
	case "pause":
		return t.setState(a.JobID, scheduler.StatePaused)
	case "resume":
		return t.setState(a.JobID, scheduler.StateScheduled)
	case "run":
		return t.runNow(ctx, a.JobID)
	case "":
		return "", fmt.Errorf("action is required")
	default:
		return "", fmt.Errorf("unknown action %q (valid: create, list, remove, pause, resume, run)", a.Action)
	}
}

func (t *manageSchedule) create(ctx context.Context, a scheduleArgs) (string, error) {
	kind := a.Kind
	if kind == "" {
		kind = scheduler.KindAgent
	}
	switch kind {
	case scheduler.KindAgent:
		if a.Prompt == "" {
			return "", fmt.Errorf("prompt is required for kind=\"agent\"")
		}
		if a.Script != "" {
			return "", fmt.Errorf("script is only valid with kind=\"script\"")
		}
	case scheduler.KindScript:
		if a.Script == "" {
			return "", fmt.Errorf("script is required for kind=\"script\"")
		}
		if a.Prompt != "" {
			return "", fmt.Errorf("prompt is only valid with kind=\"agent\"")
		}
		if t.dataDir == "" {
			return "", fmt.Errorf("script jobs require a data directory — not available in this context")
		}
	default:
		return "", fmt.Errorf("unknown kind %q (valid: agent, script)", a.Kind)
	}
	if a.Schedule == "" {
		return "", fmt.Errorf("schedule is required")
	}
	origin := session.IDFrom(ctx)
	if origin == "" {
		return "", fmt.Errorf("cannot determine session origin — manage_schedule is not callable from contexts without a session")
	}
	if strings.HasPrefix(origin, "cron:") {
		return "", fmt.Errorf("cron jobs cannot schedule more cron jobs (origin=%s)", origin)
	}
	sch, err := scheduler.ParseSchedule(a.Schedule)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	job := scheduler.Job{
		Name:      a.Name,
		Kind:      kind,
		Prompt:    a.Prompt,
		Schedule:  sch,
		Origin:    origin,
		State:     scheduler.StateScheduled,
		CreatedAt: now,
		NextRunAt: sch.NextRun(time.Time{}, now),
	}
	saved, err := t.store.Add(job)
	if err != nil {
		return "", err
	}

	// Script jobs need their body written to disk after the job ID is
	// assigned; we then update the job with the resolved path. If anything
	// here fails, roll back the job so we don't leave a broken record.
	if kind == scheduler.KindScript {
		rel := filepath.Join("schedule", "scripts", saved.ID+".py")
		abs := filepath.Join(t.dataDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			_ = t.store.Remove(saved.ID)
			return "", fmt.Errorf("create scripts dir: %w", err)
		}
		if err := os.WriteFile(abs, []byte(a.Script), 0o755); err != nil {
			_ = t.store.Remove(saved.ID)
			return "", fmt.Errorf("write script: %w", err)
		}
		updated, err := t.store.Update(saved.ID, func(j *scheduler.Job) { j.Script = rel })
		if err != nil {
			_ = os.Remove(abs)
			_ = t.store.Remove(saved.ID)
			return "", err
		}
		saved = updated
	}

	return formatJob(*saved), nil
}

func (t *manageSchedule) list() (string, error) {
	jobs := t.store.List()
	if len(jobs) == 0 {
		return "(no scheduled jobs)", nil
	}
	var b strings.Builder
	for _, j := range jobs {
		b.WriteString(formatJob(j))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *manageSchedule) remove(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("job_id is required")
	}
	// Capture the job so we can clean up its script file after removal.
	// A missing job here will surface as ErrNotFound from store.Remove below.
	scriptPath := ""
	if j := t.store.Get(id); j != nil && j.Script != "" && t.dataDir != "" {
		scriptPath = filepath.Join(t.dataDir, j.Script)
	}
	if err := t.store.Remove(id); err != nil {
		return "", err
	}
	if scriptPath != "" {
		_ = os.Remove(scriptPath)
	}
	return fmt.Sprintf("Removed %s", id), nil
}

func (t *manageSchedule) setState(id, state string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("job_id is required")
	}
	updated, err := t.store.Update(id, func(j *scheduler.Job) {
		j.State = state
		if state == scheduler.StateScheduled {
			j.LastError = ""
		}
	})
	if err != nil {
		return "", err
	}
	if t.sched != nil && state == scheduler.StateScheduled {
		_ = t.sched.SetNextRun(id)
		updated = t.store.Get(id)
	}
	return formatJob(*updated), nil
}

func (t *manageSchedule) runNow(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("job_id is required")
	}
	if t.sched == nil {
		return "", fmt.Errorf("scheduler not running")
	}
	if err := t.sched.FireNow(ctx, id); err != nil {
		return "", err
	}
	updated := t.store.Get(id)
	if updated == nil {
		return fmt.Sprintf("Fired %s", id), nil
	}
	return "Fired:\n" + formatJob(*updated), nil
}

func formatJob(j scheduler.Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- id: %s", j.ID)
	if j.Name != "" {
		fmt.Fprintf(&b, "  name: %q", j.Name)
	}
	fmt.Fprintf(&b, "  kind: %s", j.EffectiveKind())
	fmt.Fprintf(&b, "  state: %s", j.State)
	fmt.Fprintf(&b, "  schedule: %s", j.Schedule.Display)
	if !j.NextRunAt.IsZero() {
		fmt.Fprintf(&b, "  next: %s", j.NextRunAt.Format(time.RFC3339))
	}
	if !j.LastRunAt.IsZero() {
		fmt.Fprintf(&b, "  last: %s", j.LastRunAt.Format(time.RFC3339))
	}
	if j.RunCount > 0 {
		fmt.Fprintf(&b, "  runs: %d", j.RunCount)
	}
	if j.LastError != "" {
		fmt.Fprintf(&b, "  last_error: %q", j.LastError)
	}
	fmt.Fprintf(&b, "\n  origin: %s", j.Origin)
	if j.Script != "" {
		fmt.Fprintf(&b, "\n  script: %s", j.Script)
	}
	if len(j.Prompt) > 0 {
		preview := j.Prompt
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		fmt.Fprintf(&b, "\n  prompt: %s", strings.ReplaceAll(preview, "\n", " "))
	}
	return b.String()
}
