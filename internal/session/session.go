package session

import (
	"sync"

	"github.com/berkaycubuk/polaris-agent/internal/llm"
)

// Store holds in-memory chat histories keyed by session ID.
type Store struct {
	mu       sync.Mutex
	sessions map[string][]llm.Message
}

// NewStore creates an empty session store.
func NewStore() *Store {
	return &Store{sessions: map[string][]llm.Message{}}
}

// Get returns a copy of the history for the given session. Returns nil if
// the session does not exist.
func (s *Store) Get(id string) []llm.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]llm.Message(nil), s.sessions[id]...)
}

// Set replaces the history for the given session.
func (s *Store) Set(id string, history []llm.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = history
}

// Delete removes a session's history.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}
