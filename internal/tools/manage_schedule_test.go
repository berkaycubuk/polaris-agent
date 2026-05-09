package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	dir := t.TempDir()
	store, err := scheduler.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	firer := &fakeFirer{}
	return &manageSchedule{store: store, sched: firer, dataDir: dir}, store, firer
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

func TestManageSchedule_CreateScript(t *testing.T) {
	tool, store, _ := newScheduleTool(t)
	body := "print('hi from a scheduled script')\n"
	args := `{"action":"create","kind":"script","schedule":"1h","name":"hi","script":` +
		jsonString(body) + `}`
	out, err := tool.Run(ctxWithSession("telegram:42"), args)
	if err != nil {
		t.Fatalf("create script: %v", err)
	}
	if !strings.Contains(out, "kind: script") {
		t.Fatalf("formatted job missing kind: %q", out)
	}
	jobs := store.List()
	if len(jobs) != 1 || jobs[0].Kind != scheduler.KindScript {
		t.Fatalf("expected one script job, got %+v", jobs)
	}
	if jobs[0].Script == "" {
		t.Fatal("Script path not stored on job")
	}
	written, err := os.ReadFile(filepath.Join(tool.dataDir, jobs[0].Script))
	if err != nil {
		t.Fatalf("read written script: %v", err)
	}
	if string(written) != body {
		t.Fatalf("script body on disk = %q, want %q", written, body)
	}
}

func TestManageSchedule_CreateScriptValidation(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	cases := map[string]string{
		"missing-script":  `{"action":"create","kind":"script","schedule":"1h"}`,
		"prompt-with-script": `{"action":"create","kind":"script","schedule":"1h","script":"x","prompt":"y"}`,
		"script-with-agent":  `{"action":"create","kind":"agent","schedule":"1h","prompt":"x","script":"y"}`,
		"unknown-kind":       `{"action":"create","kind":"weird","schedule":"1h","prompt":"x"}`,
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tool.Run(ctxWithSession("cli"), args); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestManageSchedule_RemoveScriptDeletesFile(t *testing.T) {
	tool, store, _ := newScheduleTool(t)
	_, err := tool.Run(ctxWithSession("cli"),
		`{"action":"create","kind":"script","schedule":"1h","script":"print(1)\n"}`)
	if err != nil {
		t.Fatal(err)
	}
	job := store.List()[0]
	scriptAbs := filepath.Join(tool.dataDir, job.Script)
	if _, err := os.Stat(scriptAbs); err != nil {
		t.Fatalf("script file should exist before remove: %v", err)
	}
	if _, err := tool.Run(ctxWithSession("cli"),
		`{"action":"remove","job_id":"`+job.ID+`"}`); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(scriptAbs); !os.IsNotExist(err) {
		t.Fatalf("script file should be deleted, stat err = %v", err)
	}
}

// jsonString quotes a Go string for embedding inside a JSON literal —
// avoids hand-escaping multi-line script bodies in test args.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestManageSchedule_BadAction(t *testing.T) {
	tool, _, _ := newScheduleTool(t)
	_, err := tool.Run(ctxWithSession("cli"), `{"action":"squelch"}`)
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}
