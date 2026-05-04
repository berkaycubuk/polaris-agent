package tools

import (
	"context"
	"encoding/json"
	"fmt"
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
	store *scheduler.Store
	sched scheduleFirer
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
				"Actions:\n" +
				"- create: schedule a new job (requires prompt + schedule)\n" +
				"- list: show all jobs (id, name, schedule, state, next run)\n" +
				"- remove: delete a job by id\n" +
				"- pause / resume: toggle whether a job fires\n" +
				"- run: fire a job immediately (for testing)\n\n" +
				"Schedule formats: \"30m\", \"2h\", \"1d\" (one-shot from now); \"every 30m\", \"every 2h\" (recurring); " +
				"or an RFC3339 timestamp like \"2026-02-03T14:00:00Z\". Cron expressions are not supported.\n\n" +
				"Each fired job runs in a fresh session with no chat history, so the prompt MUST be self-contained — " +
				"include any context the job needs. Do not schedule jobs that schedule more jobs (recursion is " +
				"forbidden). To stop a job the user no longer wants, list first to find the id, then remove.",
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
					"prompt": map[string]any{
						"type":        "string",
						"description": "Required for create. The full self-contained prompt the future agent run will execute.",
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
	Prompt   string `json:"prompt"`
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
	if a.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
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
	if err := t.store.Remove(id); err != nil {
		return "", err
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
	if len(j.Prompt) > 0 {
		preview := j.Prompt
		if len(preview) > 120 {
			preview = preview[:120] + "…"
		}
		fmt.Fprintf(&b, "\n  prompt: %s", strings.ReplaceAll(preview, "\n", " "))
	}
	return b.String()
}
