package tracker

import (
	"sync"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Tracker tracks which sessions are subscribed to which streams (presence).

// A stream is a named multicast group, e.g. "chat:lobby".
type Tracker struct {
	mu      sync.RWMutex
	streams map[string]map[uuid.UUID]*session.Session
}

func New() *Tracker {
	return &Tracker{streams: make(map[string]map[uuid.UUID]*session.Session)}
}

// Join subscribes a session to a stream
func (t *Tracker) Join(stream string, s *session.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.streams[stream] == nil {
		t.streams[stream] = make(map[uuid.UUID]*session.Session)
	}
	t.streams[stream][s.ID] = s
}

// Leave unsubscribes a session from one stream
func (t *Tracker) Leave(stream string, s *session.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if members, ok := t.streams[stream]; ok {
		delete(members, s.ID)
		if len(members) == 0 {
			delete(t.streams, stream)
		}
	}
}

// LeaveAll removes a session from every stream.
func (t *Tracker) LeaveAll(s *session.Session) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for stream, members := range t.streams {
		delete(members, s.ID)
		if len(members) == 0 {
			delete(t.streams, stream)
		}
	}
}

// List returns the sessions currently in a stream
func (t *Tracker) List(stream string) []*session.Session {
	t.mu.RLock()
	defer t.mu.RUnlock()
	members := t.streams[stream]
	out := make([]*session.Session, 0, len(members))
	for _, s := range members {
		out = append(out, s)
	}
	return out
}

// Streams returns the names of all non-empty streams.
func (t *Tracker) Streams() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.streams))
	for name := range t.streams {
		out = append(out, name)
	}
	return out
}
