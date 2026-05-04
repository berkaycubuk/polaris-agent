package scheduler

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func newTestJob(name string) Job {
	in := 30 * time.Minute
	runAt := time.Now().UTC().Add(in)
	return Job{
		Name:      name,
		Prompt:    "do the thing",
		Schedule:  Schedule{Kind: "once", RunAt: &runAt, Display: "once in 30m"},
		Origin:    "telegram:42",
		State:     StateScheduled,
		CreatedAt: time.Now().UTC(),
	}
}

func TestStore_AddListGet(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	j, err := s.Add(newTestJob("hydrate"))
	if err != nil {
		t.Fatal(err)
	}
	if j.ID == "" {
		t.Fatal("expected generated ID")
	}
	got := s.Get(j.ID)
	if got == nil || got.Name != "hydrate" {
		t.Fatalf("Get returned wrong job: %+v", got)
	}
	all := s.List()
	if len(all) != 1 {
		t.Fatalf("expected 1 job, got %d", len(all))
	}
}

func TestStore_PersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewStore(dir)
	j, _ := s1.Add(newTestJob("a"))

	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Get(j.ID)
	if got == nil {
		t.Fatal("expected job to persist across store instances")
	}
	if got.Name != "a" {
		t.Fatalf("name lost on reload: %q", got.Name)
	}
}

func TestStore_Update(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	j, _ := s.Add(newTestJob("a"))

	updated, err := s.Update(j.ID, func(jj *Job) {
		jj.State = StatePaused
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != StatePaused {
		t.Fatalf("expected paused, got %s", updated.State)
	}
	if got := s.Get(j.ID); got.State != StatePaused {
		t.Fatal("update not persisted")
	}
}

func TestStore_Update_NotFound(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	_, err := s.Update("nope", func(*Job) {})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStore_Remove(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	j, _ := s.Add(newTestJob("a"))
	if err := s.Remove(j.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.Get(j.ID); got != nil {
		t.Fatal("expected nil after remove")
	}
	if err := s.Remove(j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove should be ErrNotFound, got %v", err)
	}
}

func TestStore_Dir(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(dir)
	if got, want := s.Dir(), filepath.Join(dir, "schedule"); got != want {
		t.Fatalf("Dir = %q, want %q", got, want)
	}
}
