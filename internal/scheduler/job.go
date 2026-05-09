package scheduler

import "time"

// Job is one scheduled task.
type Job struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Kind      string    `json:"kind,omitempty"` // "agent" (default) or "script"
	Prompt    string    `json:"prompt,omitempty"`
	Script    string    `json:"script,omitempty"` // path relative to dataDir, set when Kind=="script"
	Schedule  Schedule  `json:"schedule"`
	Origin    string    `json:"origin"` // session ID at create — also the delivery target
	State     string    `json:"state"`  // "scheduled", "paused", "done"
	CreatedAt time.Time `json:"created_at"`
	LastRunAt time.Time `json:"last_run_at,omitzero"`
	NextRunAt time.Time `json:"next_run_at,omitzero"`
	LastError string    `json:"last_error,omitempty"`
	RunCount  int       `json:"run_count"`
}

const (
	StateScheduled = "scheduled"
	StatePaused    = "paused"
	StateDone      = "done"

	KindAgent  = "agent"
	KindScript = "script"
)

// EffectiveKind returns the job's kind, defaulting to KindAgent for jobs
// that pre-date the field.
func (j *Job) EffectiveKind() string {
	if j.Kind == "" {
		return KindAgent
	}
	return j.Kind
}

// Active returns true if the scheduler should consider this job for firing.
func (j *Job) Active() bool {
	return j.State == StateScheduled
}
