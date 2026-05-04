package scheduler

import "time"

// Job is one scheduled task.
type Job struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	Prompt    string    `json:"prompt"`
	Schedule  Schedule  `json:"schedule"`
	Origin    string    `json:"origin"`           // session ID at create — also the delivery target
	State     string    `json:"state"`            // "scheduled", "paused", "done"
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
)

// Active returns true if the scheduler should consider this job for firing.
func (j *Job) Active() bool {
	return j.State == StateScheduled
}
