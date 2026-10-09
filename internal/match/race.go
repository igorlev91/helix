package match

import (
	"encoding/json"
	"time"

	"helix/internal/session"
)

// RaceHandler is the Bull Run race match: players submit "move" deltas,
// the server clamps them (anti-cheat speed cap) and owns the truth about
// positions. First to reach 100 wins.
//
// This replaces client-authoritative movement: a hacked client can send
// move=999, but the server will only ever apply maxSpeed per second.
const (
	raceDistance = 100.0
	maxSpeed     = 8.0 // units per second, hard server cap
)

type raceInput struct {
	Move float64 `json:"move"` // requested forward movement for this tick window
}

type racePlayer struct {
	userID     string
	username   string
	left       bool
	progress   float64
	rank       int
	finished   bool
	lastMoveAt time.Time // server clock for speed-cap validation
}

type RaceHandler struct {
	players  map[string]*racePlayer // by session id
	rankNext int
}

func NewRaceHandler() *RaceHandler {
	return &RaceHandler{players: map[string]*racePlayer{}, rankNext: 1}
}

func (h *RaceHandler) Init(m *Match) {
	now := time.Now()
	for _, s := range m.Presences() {
		h.players[s.ID.String()] = &racePlayer{userID: s.UserID.String(), username: s.Username, lastMoveAt: now}
	}
}

func (h *RaceHandler) OnData(m *Match, msg DataMessage) {
	p, ok := h.players[msg.From.ID.String()]
	if !ok || p.finished {
		return
	}
	var in raceInput
	if json.Unmarshal(msg.Data, &in) != nil || in.Move <= 0 {
		return
	}
	// Anti-cheat: movement budget is bounded by real elapsed time on the
	// server clock. Spamming move=999 a hundred times per second yields at
	// most maxSpeed units per second, no matter what the client sends.
	now := time.Now()
	elapsed := now.Sub(p.lastMoveAt).Seconds()
	p.lastMoveAt = now
	budget := elapsed * maxSpeed
	if in.Move > budget {
		in.Move = budget
	}
	p.progress += in.Move
	if p.progress >= raceDistance {
		p.progress = raceDistance
		p.finished = true
		p.rank = h.rankNext
		h.rankNext++
		m.Broadcast(mustEnvelope("race.finish", map[string]any{
			"username": p.username,
			"rank":     p.rank,
		}))
	}
}

func (h *RaceHandler) OnLeave(m *Match, s *session.Session) {
	if p, ok := h.players[s.ID.String()]; ok {
		p.left = true
	}
	m.Broadcast(mustEnvelope("race.leave", map[string]any{"username": s.Username}))
}

func (h *RaceHandler) Loop(m *Match, tick int64) bool {
	// Broadcast positions every tick.
	state := map[string]any{"tick": tick}
	for _, p := range h.players {
		state[p.username] = map[string]any{
			"progress": p.progress,
			"rank":     p.rank,
			"left":     p.left,
		}
	}
	m.Broadcast(mustEnvelope("race.state", state))

	// The match ends when no active players remain.
	active := 0
	for _, p := range h.players {
		if !p.finished && !p.left {
			active++
		}
	}
	return active > 0
}

// RaceResult is one finished player's outcome.
type RaceResult struct {
	UserID   string
	Username string
	Rank     int
}

// Results returns finished players for persistence (leaderboard write).
func (h *RaceHandler) Results() []RaceResult {
	var out []RaceResult
	for _, p := range h.players {
		if p.rank > 0 {
			out = append(out, RaceResult{UserID: p.userID, Username: p.username, Rank: p.rank})
		}
	}
	return out
}

func mustEnvelope(op string, data any) []byte {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	out, err := json.Marshal(struct {
		Op   string          `json:"op"`
		Data json.RawMessage `json:"data"`
	}{Op: op, Data: raw})
	if err != nil {
		return nil
	}
	return out
}
