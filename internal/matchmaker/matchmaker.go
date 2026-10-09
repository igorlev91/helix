package matchmaker

import (
	"sync"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Ticket is one session waiting for a match.
type Ticket struct {
	Session *session.Session
	Min     int
	Max     int
}

// Matchmaker is a FIFO ticket queue that forms matches when enough
// compatible tickets are waiting.
type Matchmaker struct {
	mu      sync.Mutex
	tickets []*Ticket // FIFO order

	// onMatch is called with the formed group
	onMatch func(tickets []*Ticket, matchID string)
}

func New(onMatch func(tickets []*Ticket, matchID string)) *Matchmaker {
	return &Matchmaker{onMatch: onMatch}
}

// Add enqueues a ticket and immediately tries to form a match.
func (m *Matchmaker) Add(t *Ticket) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.tickets {
		if existing.Session.ID == t.Session.ID {
			return // already queued
		}
	}
	m.tickets = append(m.tickets, t)
	m.tryMatchLocked()
}

// Remove cancels a session's ticket
func (m *Matchmaker) Remove(sessionID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(sessionID)
}

func (m *Matchmaker) removeLocked(sessionID uuid.UUID) {
	for i, t := range m.tickets {
		if t.Session.ID == sessionID {
			m.tickets = append(m.tickets[:i], m.tickets[i+1:]...)
			return
		}
	}
}

func (m *Matchmaker) QueueLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tickets)
}

// tryMatchLocked forms the largest possible FIFO group:
// take the oldest ticket, then keep adding tickets while the group size
// stays within everyone's min/max bounds.
func (m *Matchmaker) tryMatchLocked() {
	for len(m.tickets) > 0 {
		first := m.tickets[0]

		group := []*Ticket{first}
		for _, t := range m.tickets[1:] {
			if len(group) >= first.Max {
				break
			}
			// Compatibility: the projected group must fit everyone's bounds.
			if len(group)+1 > t.Max {
				continue
			}
			group = append(group, t)
		}

		// Everyone's min must be satisfied by the formed group.
		ok := len(group) >= first.Min
		for _, t := range group {
			if len(group) < t.Min {
				ok = false
				break
			}
		}
		if !ok {
			return // head of queue cannot match yet; wait for more tickets
		}

		for _, t := range group {
			m.removeLocked(t.Session.ID)
		}
		if m.onMatch != nil {
			m.onMatch(group, uuid.NewString())
		}
	}
}
