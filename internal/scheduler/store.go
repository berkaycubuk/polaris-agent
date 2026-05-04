package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store persists jobs to <dataDir>/schedule/jobs.json with atomic rewrites.
// All exported methods are safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	jobs map[string]*Job
}

// NewStore opens (or creates) the on-disk store under dataDir.
func NewStore(dataDir string) (*Store, error) {
	dir := filepath.Join(dataDir, "schedule")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create schedule dir: %w", err)
	}
	s := &Store{
		path: filepath.Join(dir, "jobs.json"),
		jobs: map[string]*Job{},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Path returns the directory holding jobs.json — useful for sibling output.
func (s *Store) Dir() string { return filepath.Dir(s.path) }

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read jobs.json: %w", err)
	}
	var disk struct {
		Jobs []Job `json:"jobs"`
	}
	if err := json.Unmarshal(b, &disk); err != nil {
		return fmt.Errorf("parse jobs.json: %w", err)
	}
	for i := range disk.Jobs {
		j := disk.Jobs[i]
		s.jobs[j.ID] = &j
	}
	return nil
}

func (s *Store) save() error {
	all := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		all = append(all, *j)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.Before(all[j].CreatedAt) })

	b, err := json.MarshalIndent(struct {
		Jobs []Job `json:"jobs"`
	}{Jobs: all}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".jobs-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Add stores a new job. The caller is responsible for setting all fields
// except ID, which Add assigns.
func (s *Store) Add(j Job) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.ID == "" {
		j.ID = newID()
	}
	if _, exists := s.jobs[j.ID]; exists {
		return nil, fmt.Errorf("job %s already exists", j.ID)
	}
	s.jobs[j.ID] = &j
	if err := s.save(); err != nil {
		delete(s.jobs, j.ID)
		return nil, err
	}
	return &j, nil
}

// Get returns a copy of the job, or nil if not found.
func (s *Store) Get(id string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	return &cp
}

// List returns all jobs sorted by creation time, oldest first.
func (s *Store) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Update applies fn to a copy of the job and persists. Returns ErrNotFound
// if the job doesn't exist.
func (s *Store) Update(id string, fn func(*Job)) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	fn(j)
	if err := s.save(); err != nil {
		return nil, err
	}
	cp := *j
	return &cp, nil
}

// Remove deletes the job. Returns ErrNotFound if the job doesn't exist.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return ErrNotFound
	}
	delete(s.jobs, id)
	return s.save()
}

// ErrNotFound is returned when an operation references an unknown job.
var ErrNotFound = errors.New("job not found")

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "job_" + hex.EncodeToString(b[:])
}
