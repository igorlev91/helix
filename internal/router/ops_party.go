package router

import (
	"encoding/json"
	"errors"

	"helix/internal/party"
	"helix/internal/session"
)

// Party ops
// Parties queue into matchmaking as one unit (the matchmaker receives one
// ticket per party, sized to the party).

type partyCreateRequest struct {
	Open    *bool `json:"open"` // default true
	MaxSize int   `json:"max_size"`
}

type partyIDRequest struct {
	PartyID string `json:"party_id"`
}

// RegisterPartyOps installs party.create / join / leave / members.
func (r *Router) RegisterPartyOps(pr *party.Registry) {
	push := func(sessions []*session.Session, data any) {
		SendToStream(sessions, "party.update", data)
	}

	r.Register("party.create", func(s *session.Session, data json.RawMessage) (any, error) {
		if _, ok := pr.OfMember(s.ID); ok {
			return nil, errors.New("already in a party")
		}
		var req partyCreateRequest
		_ = json.Unmarshal(data, &req)
		open := req.Open == nil || *req.Open
		p := pr.Create(s, open, req.MaxSize)
		return map[string]any{"party_id": p.ID, "open": p.Open, "max_size": p.MaxSize}, nil
	})

	r.Register("party.join", func(s *session.Session, data json.RawMessage) (any, error) {
		if _, ok := pr.OfMember(s.ID); ok {
			return nil, errors.New("already in a party")
		}
		var req partyIDRequest
		if err := json.Unmarshal(data, &req); err != nil || req.PartyID == "" {
			return nil, errors.New("party_id required")
		}
		p, ok := pr.Get(req.PartyID)
		if !ok {
			return nil, errors.New("party not found")
		}
		if !p.Join(s) {
			return nil, errors.New("party is closed or full")
		}
		pr.RegisterMember(p.ID, s.ID)
		push(p.Members(), map[string]any{"party_id": p.ID, "joined": s.Username})
		return map[string]any{"party_id": p.ID, "leader": p.Leader.Username}, nil
	})

	r.Register("party.leave", func(s *session.Session, data json.RawMessage) (any, error) {
		p, ok := pr.OfMember(s.ID)
		if !ok {
			return nil, errors.New("not in a party")
		}
		members := p.Members()
		closed, newLeader := pr.Leave(p, s)
		data2 := map[string]any{"party_id": p.ID, "left": s.Username, "closed": closed}
		if newLeader != nil {
			data2["new_leader"] = newLeader.Username
		}
		push(members, data2)
		return map[string]bool{"left": true}, nil
	})

	r.Register("party.members", func(s *session.Session, data json.RawMessage) (any, error) {
		p, ok := pr.OfMember(s.ID)
		if !ok {
			return nil, errors.New("not in a party")
		}
		names := []string{}
		for _, m := range p.Members() {
			names = append(names, m.Username)
		}
		return map[string]any{"party_id": p.ID, "leader": p.Leader.Username, "members": names}, nil
	})
}
