package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/berkaycubuk/polaris-agent/internal/scheduler"
	"github.com/berkaycubuk/polaris-agent/internal/session"
)

type fakeFirer struct {
	fired   []string
	nextRun []string
}

func (f *fakeFirer) FireNow(_ context.Context, jobID string) error {
	f.fired = append(f.fired, jobID)
	return nil
}
func (f *fakeFirer) SetNextRun(jobID string) error {
	f.nextRun = append(f.nextRun, jobID)
	return nil
}

func newScheduleTool(t *testing.T) (*manageSchedule, *scheduler.Store, *fakeFirer) {
	t.Helper()
	store, err := scheduler.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firer := &fakeFirer{}
	return &manageSchedule{store: store, sched: firer}, store, firer
}

func ctxWithSession(id string) context.Context {
	return session.WithID(context.Background(), id)
}

func TestManageSchedule_Create(t *testing.T) {
	tool, store, _ := newScheduleTool(t)
	out, err := tool.Run(ctxWithSession("telegram:42"),
		`{"action":"create","prompt":"check feed","schedule":"30m","name":"feed"}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out, "id: job_") {
		t.Fatalf("output missing id: %q", out)
	}
	jobs := store.List()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}
	if jobs[0].Origin != "telegram:42" {
		t.Fatalf("origin = %q, want telegram:42", jobs[0].Origin)
	}
	if jobs[0].Schedule.Kind != "once" {
		t.Fatalf("kind = %q, want once", jobs[0].Schedule.Kind)
	}
}

func TestManageSchedule_CreateRequiresSession(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	_, err := tool.Run(context.Background(),
		`{"action":"create","prompt":"x","schedule":"30m"}`)
	if err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("expected session-origin error, got %v", err)
	}
}

func TestManageSchedule_CreateRefusesRecursive(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	_, err := tool.Run(ctxWithSession("cron:job_abc"),
		`{"action":"create","prompt":"x","schedule":"30m"}`)
	if err == nil || !strings.Contains(err.Error(), "recursive") &&
		!strings.Contains(err.Error(), "cron jobs cannot schedule") {
		t.Fatalf("expected recursive-block error, got %v", err)
	}
}

func TestManageSchedule_CreateRequiresFields(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	cases := []string{
		`{"action":"create","schedule":"30m"}`,                 // missing prompt
		`{"action":"create","prompt":"x"}`,                     // missing schedule
		`{"action":"create","prompt":"x","schedule":"banana"}`, // bad schedule
	}
	for _, c := range cases {
		if _, err := tool.Run(ctxWithSession("cli"), c); err == nil {
			t.Errorf("expected error for %s", c)
		}
	}
}

func TestManageSchedule_ListEmpty(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	out, err := tool.Run(ctxWithSession("cli"), `{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no scheduled") {
		t.Fatalf("expected empty message, got %q", out)
	}
}

func TestManageSchedule_PauseResumeRemove(t *testing.T) {
	tool, store, firer := newScheduleTool(t)
	_, err := tool.Run(ctxWithSession("cli"),
		`{"action":"create","prompt":"x","schedule":"every 1h"}`)
	if err != nil {
		t.Fatal(err)
	}
	id := store.List()[0].ID

	if _, err := tool.Run(ctxWithSession("cli"),
		`{"action":"pause","job_id":"`+id+`"}`); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if got := store.Get(id).State; got != scheduler.StatePaused {
		t.Fatalf("state = %s, want paused", got)
	}

	if _, err := tool.Run(ctxWithSession("cli"),
		`{"action":"resume","job_id":"`+id+`"}`); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got := store.Get(id).State; got != scheduler.StateScheduled {
		t.Fatalf("state = %s, want scheduled", got)
	}
	// resume should have triggered SetNextRun on the firer.
	if len(firer.nextRun) != 1 || firer.nextRun[0] != id {
		t.Fatalf("expected SetNextRun called once with %s, got %v", id, firer.nextRun)
	}

	if _, err := tool.Run(ctxWithSession("cli"),
		`{"action":"remove","job_id":"`+id+`"}`); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if store.Get(id) != nil {
		t.Fatal("job not removed")
	}
}

func TestManageSchedule_Run(t *testing.T) {
	tool, store, firer := newScheduleTool(t)
	_, err := tool.Run(ctxWithSession("cli"),
		`{"action":"create","prompt":"x","schedule":"every 1h"}`)
	if err != nil {
		t.Fatal(err)
	}
	id := store.List()[0].ID
	if _, err := tool.Run(ctxWithSession("cli"),
		`{"action":"run","job_id":"`+id+`"}`); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(firer.fired) != 1 || firer.fired[0] != id {
		t.Fatalf("expected firer.FireNow called with %s, got %v", id, firer.fired)
	}
}

func TestManageSchedule_BadAction(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	_, err := tool.Run(ctxWithSession("cli"), `{"action":"squelch"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}
