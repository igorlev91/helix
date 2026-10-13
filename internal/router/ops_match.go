package router

import (
	"encoding/json"
	"errors"

	"helix/internal/auth"
	"helix/internal/match"
	"helix/internal/session"
	"helix/internal/tracker"
)

// Match ops.
// match.join: enter a match with the short-lived match token from
// match.found (Nakama: joinMatch(token) — the token proves the matchmaker
// put you in this match and expires quickly).
// match.data: send input to a match; the authoritative loop consumes it.

type matchJoinRequest struct {
	Token string `json:"token"`
}

type matchDataRequest struct {
	MatchID string          `json:"match_id"`
	Data    json.RawMessage `json:"data"`
}

// RegisterMatchOps installs match.join / match.data.
func (r *Router) RegisterMatchOps(matches *match.Registry, tr *tracker.Tracker, secret string) {
	r.Register("match.join", func(s *session.Session, data json.RawMessage) (any, error) {
		var req matchJoinRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Token == "" {
			return nil, errors.New("token required")
		}
		claims, ok := auth.ParseMatchToken(secret, req.Token)
		if !ok {
			return nil, errors.New("invalid or expired match token")
		}
		// The token authorizes a specific user; it is not transferable.
		if claims.UserID != s.UserID.String() {
			return nil, errors.New("token belongs to another user")
		}
		m, err := matches.Join(claims.MatchID, s)
		if err != nil {
			return nil, err
		}
		// Joining the match also subscribes to its chat channel.
		channel := "match-" + m.ID
		tr.Join("chat:"+channel, s)
		return map[string]any{"match_id": m.ID, "channel": channel}, nil
	})

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
