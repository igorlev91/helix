package router

import (
	"encoding/json"
	"errors"

	"helix/internal/match"
	"helix/internal/matchmaker"
	"helix/internal/session"
	"helix/internal/tracker"
)

// Matchmaker ops
// When a match forms, members join the "chat:match-<id>" stream and get a
// match.found push; the match channel then works with regular chat ops.

type mmAddRequest struct {
	Min int `json:"min"` // players needed to start
	Max int `json:"max"` // max group size (Nakama: max_count)
}

// RegisterMatchmakerOps installs matchmaker.add / matchmaker.remove.
func (r *Router) RegisterMatchmakerOps(mm *matchmaker.Matchmaker) {
	r.Register("matchmaker.add", func(s *session.Session, data json.RawMessage) (any, error) {
		var req mmAddRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, errors.New("min and max required")
		}
		if req.Min < 2 {
			req.Min = 2
		}
		if req.Max < req.Min {
			req.Max = req.Min
		}
		mm.Add(&matchmaker.Ticket{Session: s, Min: req.Min, Max: req.Max})
		return map[string]any{"queued": true, "min": req.Min, "max": req.Max}, nil
	})

	r.Register("matchmaker.remove", func(s *session.Session, data json.RawMessage) (any, error) {
		mm.Remove(s.ID)
		return map[string]bool{"removed": true}, nil
	})
}

// NewMatchCallback builds the onMatch callback for the matchmaker:
// creates an authoritative race match, joins members into its chat stream
// and pushes match.found (Nakama: matchRegistry.Create + tracker.Track +
// router matchmaker.matched).
func NewMatchCallback(tr *tracker.Tracker, matches *match.Registry) func(tickets []*matchmaker.Ticket, matchID string) {
	return func(tickets []*matchmaker.Ticket, matchID string) {
		channel := "match-" + matchID
		usernames := make([]string, 0, len(tickets))
		players := make([]*session.Session, 0, len(tickets))
		for _, t := range tickets {
			tr.Join("chat:"+channel, t.Session)
			usernames = append(usernames, t.Session.Username)
			players = append(players, t.Session)
		}

		// Authoritative match with a 5 Hz server tick.
		matches.Create(matchID, 5, match.NewRaceHandler(), players)

		for _, t := range tickets {
			payload, err := json.Marshal(Envelope{Op: "match.found", Data: mustJSON(map[string]any{
				"match_id": matchID,
				"channel":  channel,
				"players":  usernames,
			})})
			if err != nil {
				continue
			}
			t.Session.Send(payload)
		}
	}
}

func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}
