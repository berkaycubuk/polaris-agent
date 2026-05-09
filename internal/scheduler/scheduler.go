package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Runner runs a one-shot agent turn for a fired job.
type Runner interface {
	// Chat starts a fresh session under sessionID and returns the agent's reply.
	// The scheduler uses a per-job session ID so cron runs don't share history
	// with the user's interactive sessions.
	Chat(ctx context.Context, sessionID, message string) (string, error)
}

// Deliverer routes a fired job's reply back to the origin (Telegram chat,
// log file, etc.). Implementations should be best-effort: a failed delivery
// must not block the scheduler from advancing.
type Deliverer interface {
	Deliver(ctx context.Context, job Job, reply string, runErr error) error
}

// Scheduler ticks at a fixed interval, fires due jobs, and delivers replies.
type Scheduler struct {
	store   *Store
	runner  Runner
	scripts ScriptRunner
	deliver Deliverer
	tick    time.Duration
	fireSem chan struct{} // bounds in-flight job runs
	wg      sync.WaitGroup
}

// New constructs a scheduler. tick is how often to scan for due jobs;
// 0 picks a sensible default (60s). maxParallel caps concurrent fires
// (default 4) so a flurry of due jobs doesn't fork-bomb the LLM.
//
// scripts may be nil — in that case script-kind jobs fail with a clear
// error at fire time rather than silently doing nothing.
func New(store *Store, runner Runner, scripts ScriptRunner, deliver Deliverer, tick time.Duration, maxParallel int) *Scheduler {
	if tick <= 0 {
		tick = time.Minute
	}
	if maxParallel <= 0 {
		maxParallel = 4
	}
	return &Scheduler{
		store:   store,
		runner:  runner,
		scripts: scripts,
		deliver: deliver,
		tick:    tick,
		fireSem: make(chan struct{}, maxParallel),
	}
}

// Run blocks until ctx is cancelled, ticking every interval. On cancel it
// waits for in-flight fires to finish before returning.
func (s *Scheduler) Run(ctx context.Context) error {
	log.Printf("scheduler started (tick=%s)", s.tick)
	t := time.NewTicker(s.tick)
	defer t.Stop()

	// Fire once immediately so a server restart doesn't leave a recently-due
	// job sitting unfired for up to a tick.
	s.tickOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			log.Printf("scheduler stopped")
			return ctx.Err()
		case <-t.C:
			s.tickOnce(ctx)
		}
	}
}

func (s *Scheduler) tickOnce(ctx context.Context) {
	now := time.Now().UTC()
	for _, j := range s.store.List() {
		if !j.Active() {
			continue
		}
		next := j.Schedule.NextRun(j.LastRunAt, now)
		if next.IsZero() {
			// One-shot whose grace window passed — mark done so it doesn't
			// linger as a perpetually-not-due job.
			if j.Schedule.Kind == "once" && j.LastRunAt.IsZero() {
				_, _ = s.store.Update(j.ID, func(jj *Job) {
					jj.State = StateDone
					jj.LastError = "missed: schedule passed before tick"
				})
			}
			continue
		}
		if next.After(now) {
			// Update NextRunAt so list output stays informative.
			if !j.NextRunAt.Equal(next) {
				_, _ = s.store.Update(j.ID, func(jj *Job) { jj.NextRunAt = next })
			}
			continue
		}
		// Due. Fire under the parallel cap.
		select {
		case s.fireSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		s.wg.Add(1)
		go func(job Job) {
			defer s.wg.Done()
			defer func() { <-s.fireSem }()
			s.fire(ctx, job)
		}(j)
	}
}

// FireNow runs a job immediately, regardless of schedule. Returns the reply
// or an error. Used by the manage_schedule "run" action.
func (s *Scheduler) FireNow(ctx context.Context, jobID string) error {
	j := s.store.Get(jobID)
	if j == nil {
		return ErrNotFound
	}
	s.fire(ctx, *j)
	return nil
}

func (s *Scheduler) fire(ctx context.Context, j Job) {
	var (
		reply  string
		runErr error
	)
	switch j.EffectiveKind() {
	case KindScript:
		if s.scripts == nil {
			runErr = fmt.Errorf("script runner not configured")
		} else {
			reply, runErr = s.scripts.RunScript(ctx, j)
		}
	default: // KindAgent
		sessionID := "cron:" + j.ID
		reply, runErr = s.runner.Chat(ctx, sessionID, j.Prompt)
	}

	now := time.Now().UTC()
	_, _ = s.store.Update(j.ID, func(jj *Job) {
		jj.LastRunAt = now
		jj.RunCount++
		if runErr != nil {
			jj.LastError = runErr.Error()
		} else {
			jj.LastError = ""
		}
		// Compute next run; one-shots with no future fire transition to done.
		next := jj.Schedule.NextRun(jj.LastRunAt, now)
		if next.IsZero() {
			jj.State = StateDone
			jj.NextRunAt = time.Time{}
		} else {
			jj.NextRunAt = next
		}
	})

	if s.deliver != nil {
		if err := s.deliver.Deliver(ctx, j, reply, runErr); err != nil {
			log.Printf("scheduler: deliver job=%s: %v", j.ID, err)
		}
	}
	if runErr != nil {
		log.Printf("scheduler: fire job=%s err=%v", j.ID, runErr)
	}
}

// SetNextRun re-computes and stores NextRunAt for a job (useful right after
// create/resume so list output is accurate immediately).
func (s *Scheduler) SetNextRun(jobID string) error {
	now := time.Now().UTC()
	_, err := s.store.Update(jobID, func(j *Job) {
		j.NextRunAt = j.Schedule.NextRun(j.LastRunAt, now)
	})
	if err != nil {
		return fmt.Errorf("set next run: %w", err)
	}
	return nil
}
