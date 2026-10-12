package matchmaker

import (
	"sync"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Ticket is one matchmaking entry. A user may hold MULTIPLE
// tickets (e.g. searching several game modes at once); each ticket has
// its own ID for cancellation and its own query + properties.
type Ticket struct {
	ID           string             // unique per entry (Nakama: ticket)
	Session      *session.Session   `json:"-"`
	Min          int                // group size incl. self, >= 2
	Max          int                // max group size, >= Min
	Query        string             // Bleve subset, see query.go
	StringProps  map[string]string  `json:"string_properties,omitempty"`
	NumericProps map[string]float64 `json:"numeric_properties,omitempty"`
}

// Matchmaker keeps a pool of waiting tickets and forms groups whose
// members mutually satisfy each other's queries and size bounds.
// All mutations happen under one mutex — sequential processing like
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
// Multiple tickets per session are allowed (unlike the pre-query version).
func (m *Matchmaker) Add(t *Ticket) *Ticket {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	m.tickets = append(m.tickets, t)
	m.tryMatchLocked()
	return t
}

// RemoveTicket cancels one ticket by ID (Nakama: RemoveMatchmaker(ticket)).
func (m *Matchmaker) RemoveTicket(ticketID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.tickets {
		if t.ID == ticketID {
			m.tickets = append(m.tickets[:i], m.tickets[i+1:]...)
			return true
		}
	}
	return false
}

// Remove cancels ALL tickets of a session (disconnect cleanup).
func (m *Matchmaker) Remove(sessionID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(sessionID)
}

func (m *Matchmaker) removeLocked(sessionID uuid.UUID) {
	kept := m.tickets[:0]
	for _, t := range m.tickets {
		if t.Session.ID != sessionID {
			kept = append(kept, t)
		}
	}
	m.tickets = kept
}

func (m *Matchmaker) QueueLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.tickets)
}

// tryMatchLocked forms the largest possible FIFO group around the oldest
// ticket: a candidate joins when its size bounds fit AND its query
// mutually matches every member already in the group.
func (m *Matchmaker) tryMatchLocked() {
	for len(m.tickets) > 0 {
		first := m.tickets[0]

		group := []*Ticket{first}
	outer:
		for _, t := range m.tickets[1:] {
			if len(group) >= first.Max {
				break
			}
			// Size compatibility: the projected group must fit everyone's bounds.
			if len(group)+1 > t.Max {
				continue
			}
			// Query compatibility: mutual against EVERY group member.
			for _, member := range group {
				if !compatible(member, t) {
					continue outer
				}
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
			m.removeLocked(t.Session.ID) // all tickets of matched sessions
		}
		if m.onMatch != nil {
			m.onMatch(group, uuid.NewString())
		}
	}
}
