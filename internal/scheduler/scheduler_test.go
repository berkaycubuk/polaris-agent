package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRunner struct {
	mu     sync.Mutex
	fired  []string
	reply  string
	err    error
}

func (f *fakeRunner) Chat(_ context.Context, sessionID, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fired = append(f.fired, sessionID)
	return f.reply, f.err
}

func (f *fakeRunner) sessions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.fired))
	copy(out, f.fired)
	return out
}

type captureDeliverer struct {
	count int32
	last  string
}

func (d *captureDeliverer) Deliver(_ context.Context, _ Job, reply string, _ error) error {
	atomic.AddInt32(&d.count, 1)
	d.last = reply
	return nil
}

func TestScheduler_FiresDueOneShot(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	past := time.Now().UTC().Add(-1 * time.Second)
	job, _ := store.Add(Job{
		Prompt:    "ping",
		Schedule:  Schedule{Kind: "once", RunAt: &past, Display: "once"},
		Origin:    "telegram:1",
		State:     StateScheduled,
		CreatedAt: time.Now().UTC(),
	})

	runner := &fakeRunner{reply: "pong"}
	deliv := &captureDeliverer{}
	s := New(store, runner, deliv, 50*time.Millisecond, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	if got := runner.sessions(); len(got) == 0 || got[0] != "cron:"+job.ID {
		t.Fatalf("expected fire under cron:%s, got %v", job.ID, got)
	}
	if atomic.LoadInt32(&deliv.count) == 0 {
		t.Fatal("deliverer was never called")
	}
	if deliv.last != "pong" {
		t.Fatalf("delivered reply = %q, want %q", deliv.last, "pong")
	}

	got := store.Get(job.ID)
	if got.State != StateDone {
		t.Fatalf("one-shot should be done after fire, got state=%s", got.State)
	}
	if got.RunCount != 1 {
		t.Fatalf("RunCount = %d, want 1", got.RunCount)
	}
}

func TestScheduler_RecordsRunError(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	past := time.Now().UTC().Add(-1 * time.Second)
	job, _ := store.Add(Job{
		Schedule: Schedule{Kind: "once", RunAt: &past},
		State:    StateScheduled,
	})
	runner := &fakeRunner{err: errors.New("boom")}
	s := New(store, runner, &captureDeliverer{}, 50*time.Millisecond, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	got := store.Get(job.ID)
	if got.LastError == "" {
		t.Fatal("expected LastError to be set on runner error")
	}
}

func TestScheduler_SkipsPaused(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	past := time.Now().UTC().Add(-1 * time.Second)
	store.Add(Job{
		Schedule: Schedule{Kind: "once", RunAt: &past},
		State:    StatePaused,
	})
	runner := &fakeRunner{}
	s := New(store, runner, &captureDeliverer{}, 50*time.Millisecond, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	if got := runner.sessions(); len(got) != 0 {
		t.Fatalf("paused job should not fire, got %v", got)
	}
}

func TestScheduler_IntervalRecurs(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	job, _ := store.Add(Job{
		Schedule:  Schedule{Kind: "interval", Interval: 100 * time.Millisecond, Display: "every 100ms"},
		State:     StateScheduled,
		LastRunAt: time.Now().UTC().Add(-1 * time.Second), // already due
	})
	runner := &fakeRunner{reply: "ok"}
	s := New(store, runner, &captureDeliverer{}, 30*time.Millisecond, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = s.Run(ctx)

	got := store.Get(job.ID)
	if got.RunCount < 2 {
		t.Fatalf("expected at least 2 runs over 500ms tick, got %d", got.RunCount)
	}
	if got.State != StateScheduled {
		t.Fatalf("interval job should stay scheduled, got %s", got.State)
	}
}

func TestScheduler_FireNow(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	future := time.Now().UTC().Add(1 * time.Hour) // not due
	job, _ := store.Add(Job{
		Schedule: Schedule{Kind: "once", RunAt: &future},
		State:    StateScheduled,
	})
	runner := &fakeRunner{reply: "manual"}
	s := New(store, runner, &captureDeliverer{}, time.Hour, 1)

	if err := s.FireNow(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if got := store.Get(job.ID).RunCount; got != 1 {
		t.Fatalf("expected RunCount=1 after FireNow, got %d", got)
	}
}

func TestScheduler_FireNow_NotFound(t *testing.T) {
	store, _ := NewStore(t.TempDir())
	s := New(store, &fakeRunner{}, &captureDeliverer{}, time.Hour, 1)
	if err := s.FireNow(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
