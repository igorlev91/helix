package party

import (
	"sync"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Party is a group of players who queue for matchmaking together.
// a leader, members, open/closed flag; the party joins matchmaking as
// one ticket with min/max scaled by party size.
type Party struct {
	ID      string
	Leader  *session.Session
	Open    bool
	MaxSize int

	mu      sync.Mutex
	members map[uuid.UUID]*session.Session // includes the leader
}

type Registry struct {
	mu       sync.Mutex
	parties  map[string]*Party
	byMember map[uuid.UUID]string // session id -> party id
}

func NewRegistry() *Registry {
	return &Registry{parties: map[string]*Party{}, byMember: map[uuid.UUID]string{}}
}

// Create makes a party with the caller as leader 
func (r *Registry) Create(leader *session.Session, open bool, maxSize int) *Party {
	if maxSize <= 0 {
		maxSize = 4
	}
	p := &Party{
		ID:      uuid.NewString(),
		Leader:  leader,
		Open:    open,
		MaxSize: maxSize,
		members: map[uuid.UUID]*session.Session{leader.ID: leader},
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parties[p.ID] = p
	r.byMember[leader.ID] = p.ID
	return p
}

func (r *Registry) Get(id string) (*Party, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.parties[id]
	return p, ok
}

// OfMember returns the party a session belongs to, if any.
func (r *Registry) OfMember(sessionID uuid.UUID) (*Party, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byMember[sessionID]
	if !ok {
		return nil, false
	}
	p, ok := r.parties[id]
	return p, ok
}

func (r *Registry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p, ok := r.parties[id]; ok {
		for sid := range p.members {
			delete(r.byMember, sid)
		}
		delete(r.parties, id)
	}
}

// Join adds a member to an open party
func (p *Party) Join(s *session.Session) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.Open || len(p.members) >= p.MaxSize {
		return false
	}
	p.members[s.ID] = s
	return true
}

// RegisterMember records the session->party index (called after Join).
func (r *Registry) RegisterMember(partyID string, sessionID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byMember[sessionID] = partyID
}

// Leave removes a member; the leader leaving closes the party and the
// next member (if any) becomes leader 
func (r *Registry) Leave(p *Party, s *session.Session) (closed bool, newLeader *session.Session) {
	p.mu.Lock()
	delete(p.members, s.ID)
	remaining := len(p.members)
	if s.ID == p.Leader.ID && remaining > 0 {
		for _, m := range p.members {
			p.Leader = m
			newLeader = m
			break
		}
	}
	p.mu.Unlock()

	r.mu.Lock()
	delete(r.byMember, s.ID)
	if remaining == 0 {
		r.removeLocked(p.ID)
		closed = true
	}
	r.mu.Unlock()
	return closed, newLeader
}

func (r *Registry) removeLocked(id string) {
	delete(r.parties, id)
}

// Members returns a snapshot of party members.
func (p *Party) Members() []*session.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*session.Session, 0, len(p.members))
	for _, s := range p.members {
		out = append(out, s)
	}
	return out
}
