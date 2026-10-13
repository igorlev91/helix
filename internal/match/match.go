package match

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"helix/internal/session"
)

// DataMessage is one input message from a player inside a match.
type DataMessage struct {
	From *session.Session
	Data []byte
}

// Handler owns the authoritative state of one match.
// Init / Join / Leave / Loop / Terminate.
type Handler interface {
	// Init runs once when the match is created.
	Init(m *Match)
	// Loop runs every tick; return false to terminate the match.
	Loop(m *Match, tick int64) bool
	// OnData handles one input message (validate + apply to state).
	OnData(m *Match, msg DataMessage)
	// OnJoin registers a player who joined after Init (match token flow:
	// the match is created on the FIRST token join, others join later).
	OnJoin(m *Match, s *session.Session)
	// OnLeave removes a player.
	OnLeave(m *Match, s *session.Session)
}

// Match is an authoritative server-side match with its own tick loop.
type Match struct {
	ID       string
	TickRate int // ticks per second 

	mu        sync.RWMutex
	presences map[uuid.UUID]*session.Session

	in       chan DataMessage
	quit     chan struct{}
	handler  Handler
	onFinish func(m *Match) // e.g. write results to a leaderboard
}

func newMatch(id string, tickRate int, h Handler, onFinish func(*Match)) *Match {
	if tickRate <= 0 {
		tickRate = 5
	}
	return &Match{
		ID:        id,
		TickRate:  tickRate,
		presences: map[uuid.UUID]*session.Session{},
		in:        make(chan DataMessage, 128),
		quit:      make(chan struct{}),
		handler:   h,
		onFinish:  onFinish,
	}
}

func (m *Match) Presences() []*session.Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*session.Session, 0, len(m.presences))
	for _, s := range m.presences {
		out = append(out, s)
	}
	return out
}

func (m *Match) AddPresence(s *session.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.presences[s.ID] = s
}

func (m *Match) RemovePresence(id uuid.UUID) {
	m.mu.Lock()
	s, ok := m.presences[id]
	delete(m.presences, id)
	m.mu.Unlock()
	if ok {
		select {
		case m.in <- DataMessage{From: s}: // empty Data = leave event
		default:
		}
	}
}

func (m *Match) Has(s *session.Session) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.presences[s.ID]
	return ok
}

// InputCh exposes the serialized input channel
func (m *Match) InputCh() chan DataMessage {
	return m.in
}

// Handler returns the match handler (for result extraction on finish).
func (m *Match) Handler() Handler {
	return m.handler
}

// Broadcast sends one payload to every presence
func (m *Match) Broadcast(payload []byte) {
	for _, s := range m.Presences() {
		s.Send(payload)
	}
}

// run is the match goroutine: all state mutations happen here, serialized
// by the input channel — the same single-writer discipline 
func (m *Match) run(registry *Registry) {
	m.handler.Init(m)
	ticker := time.NewTicker(time.Second / time.Duration(m.TickRate))
	defer ticker.Stop()
	var tick int64
	for {
		select {
		case <-m.quit:
			return
		case msg := <-m.in:
			if len(msg.Data) == 0 {
				m.handler.OnLeave(m, msg.From)
			} else {
				m.handler.OnData(m, msg)
			}
		case <-ticker.C:
			tick++
			if !m.handler.Loop(m, tick) {
				if m.onFinish != nil {
					m.onFinish(m)
				}
				registry.Remove(m.ID)
				log.Printf("match %s terminated after %d ticks", m.ID, tick)
				return
			}
		}
	}
}

func (m *Match) Stop() {
	select {
	case <-m.quit:
	default:
		close(m.quit)
	}
}

// Registry tracks live matches and pending matches awaiting token joins.
type Registry struct {
	mu       sync.RWMutex
	matches  map[string]*Match
	pending  map[string]*PendingMatch
	onFinish func(*Match)
}

// PendingMatch is a match reserved by the matchmaker: created when the
// first matched user joins with a valid token 
type PendingMatch struct {
	TickRate int
	Factory  func() Handler
	Allowed  map[uuid.UUID]bool // user IDs the matchmaker matched
}

func NewRegistry(onFinish func(*Match)) *Registry {
	return &Registry{
		matches:  map[string]*Match{},
		pending:  map[string]*PendingMatch{},
		onFinish: onFinish,
	}
}

// Create spawns a match with its own loop goroutine.
func (r *Registry) Create(id string, tickRate int, h Handler, players []*session.Session) *Match {
	m := newMatch(id, tickRate, h, r.onFinish)
	for _, p := range players {
		p.MatchID = m.ID
		m.AddPresence(p)
	}
	r.mu.Lock()
	r.matches[m.ID] = m
	r.mu.Unlock()
	go m.run(r)
	log.Printf("match %s created with %d players", m.ID, len(players))
	return m
}

// RegisterPending reserves a match for token-authorized joins.
func (r *Registry) RegisterPending(id string, tickRate int, factory func() Handler, allowed []uuid.UUID) {
	set := make(map[uuid.UUID]bool, len(allowed))
	for _, u := range allowed {
		set[u] = true
	}
	r.mu.Lock()
	r.pending[id] = &PendingMatch{TickRate: tickRate, Factory: factory, Allowed: set}
	r.mu.Unlock()
}

// ErrNotAllowed / ErrNoMatch describe join failures.
var (
	ErrNoMatch    = errors.New("match not found")
	ErrNotAllowed = errors.New("not matched into this match")
)

// Join enters a match with a validated match token: the user must be in
// the allowed set. The match is created lazily on the first join.
func (r *Registry) Join(id string, s *session.Session) (*Match, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.matches[id]; ok {
		s.MatchID = id
		m.AddPresence(s)
		m.handler.OnJoin(m, s)
		return m, nil
	}
	p, ok := r.pending[id]
	if !ok {
		return nil, ErrNoMatch
	}
	if !p.Allowed[s.UserID] {
		return nil, ErrNotAllowed
	}
	m := newMatch(id, p.TickRate, p.Factory(), r.onFinish)
	s.MatchID = id
	m.AddPresence(s)
	r.matches[id] = m
	go m.run(r)
	m.handler.OnJoin(m, s) // Init registered only presences present at start
	log.Printf("match %s created on first token join", id)
	return m, nil
}

func (r *Registry) Get(id string) (*Match, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.matches[id]
	return m, ok
}

func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.matches, id)
	delete(r.pending, id)
}
