package router

import (
	"encoding/json"
	"errors"

	"helix/internal/match"
	"helix/internal/session"
)

// Match data op
// the client sends input to a match, the authoritative loop consumes it.

type matchDataRequest struct {
	MatchID string          `json:"match_id"`
	Data    json.RawMessage `json:"data"`
}

// RegisterMatchOps installs match.data.
func (r *Router) RegisterMatchOps(matches *match.Registry) {
	r.Register("match.data", func(s *session.Session, data json.RawMessage) (any, error) {
		var req matchDataRequest
		if err := json.Unmarshal(data, &req); err != nil || req.MatchID == "" {
			return nil, errors.New("match_id required")
		}
		m, ok := matches.Get(req.MatchID)
		if !ok {
			return nil, errors.New("match not found")
		}
		if !m.Has(s) {
			return nil, errors.New("not in match")
		}
		// Feed the input channel; the match loop applies it on its goroutine

		select {
		case m.InputCh() <- match.DataMessage{From: s, Data: req.Data}:
			return map[string]bool{"accepted": true}, nil
		default:
			return nil, errors.New("match input queue full")
		}
	})
}
