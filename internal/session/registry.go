package session

import (
	"sync"

	"github.com/google/uuid"
)

// Registry tracks online sessions.
// Simplified analog of session_registry.go from Nakama:
// Add on connect, Remove on disconnect, lookup by ID.
type Registry struct {
	mu       sync.RWMutex
	sessions map[uuid.UUID]*Session
}

func NewRegistry() *Registry {
	return &Registry{sessions: make(map[uuid.UUID]*Session)}
}

func (r *Registry) Add(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
}

func (r *Registry) Remove(id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

func (r *Registry) Get(id uuid.UUID) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return s, ok
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// IDs returns the IDs of all online sessions.
// Used by the debug endpoint and later by presence (tracker).
func (r *Registry) IDs() []uuid.UUID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]uuid.UUID, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	return ids
}
