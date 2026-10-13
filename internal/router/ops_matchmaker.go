package router

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"helix/internal/auth"
	"helix/internal/match"
	"helix/internal/matchmaker"
	"helix/internal/session"
	"helix/internal/tracker"
)

// Matchmaker ops
// Entries carry properties + a query (Bleve subset); matching is
// mutual and min/max counts include the searching user. When a match forms,
// members join the "chat:match-<id>" stream and get a match.found push with
// the matched users' properties

type mmAddRequest struct {
	Min          int                `json:"min"` // players incl. self, >= 2
	Max          int                `json:"max"` // max group size (Nakama: max_count)
	Query        string             `json:"query"`
	StringProps  map[string]string  `json:"string_properties"`
	NumericProps map[string]float64 `json:"numeric_properties"`
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
		if req.Query == "" {
			req.Query = "*"
		}
		t := mm.Add(&matchmaker.Ticket{
			Session:      s,
			Min:          req.Min,
			Max:          req.Max,
			Query:        req.Query,
			StringProps:  req.StringProps,
			NumericProps: req.NumericProps,
		})
		// Nakama returns the ticket; the client needs it for cancellation.
		return map[string]any{"ticket": t.ID, "min": req.Min, "max": req.Max}, nil
	})

	// matchmaker.remove {ticket}: cancel one entry; empty ticket = all mine.
	r.Register("matchmaker.remove", func(s *session.Session, data json.RawMessage) (any, error) {
		var req struct {
			Ticket string `json:"ticket"`
		}
		_ = json.Unmarshal(data, &req)
		if req.Ticket == "" {
			mm.Remove(s.ID)
			return map[string]bool{"removed": true}, nil
		}
		return map[string]bool{"removed": mm.RemoveTicket(req.Ticket)}, nil
	})
}

// NewMatchCallback builds the onMatch callback for the matchmaker:
// reserves a pending match and issues a SHORT-LIVED match token per user
// (Nakama: MatchmakerMatched.token — a JWT authorizing match join; the
// match starts when clients join with their tokens).
func NewMatchCallback(tr *tracker.Tracker, matches *match.Registry, secret string) func(tickets []*matchmaker.Ticket, matchID string) {
	return func(tickets []*matchmaker.Ticket, matchID string) {
		channel := "match-" + matchID
		// Matched users see each other's properties (Nakama: MatchmakerMatched).
		type matchedUser struct {
			Username     string             `json:"username"`
			StringProps  map[string]string  `json:"string_properties,omitempty"`
			NumericProps map[string]float64 `json:"numeric_properties,omitempty"`
		}
		users := make([]matchedUser, 0, len(tickets))
		allowed := make([]uuid.UUID, 0, len(tickets))
		for _, t := range tickets {
			users = append(users, matchedUser{t.Session.Username, t.StringProps, t.NumericProps})
			allowed = append(allowed, t.Session.UserID)
		}

		// Reserve the match; it starts on the first token join.
		matches.RegisterPending(matchID, 5, func() match.Handler { return match.NewRaceHandler() }, allowed)

		for _, t := range tickets {
			token, err := auth.GenerateMatchToken(secret, t.Session.UserID, matchID, 30*time.Second)
			if err != nil {
				continue
			}
			payload, err := json.Marshal(Envelope{Op: "match.found", Data: mustJSON(map[string]any{
				"match_id": matchID,
				"channel":  channel,
				"ticket":   t.ID,  // which of my tickets matched
				"token":    token, // short-lived join authorization
				"users":    users,
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
