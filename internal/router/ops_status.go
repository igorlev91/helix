package router

import (
	"encoding/json"

	"helix/internal/session"
	"helix/internal/status"
)

// Status presence ops
// Friends follow each other; when I update my status or come online,
// my followers get a "status.update" push.

type statusSetRequest struct {
	Message string `json:"message"`
}

// RegisterStatusOps installs status.set / status.get.
func (r *Router) RegisterStatusOps(sr *status.Registry, registry *session.Registry) {
	r.Register("status.set", func(s *session.Session, data json.RawMessage) (any, error) {
		var req statusSetRequest
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, err
		}
		s.Status = req.Message
		sr.Set(s.UserID, req.Message)
		PushPresence(sr, registry, s, true)
		return map[string]string{"status": req.Message}, nil
	})

	// status.get — statuses of everyone I follow (friend presence list).
	r.Register("status.get", func(s *session.Session, data json.RawMessage) (any, error) {
		type entry struct {
			UserID string `json:"user_id"`
			Online bool   `json:"online"`
			Status string `json:"status"`
		}
		var out []entry
		for _, f := range sr.FollowingOf(s.UserID) {
			out = append(out, entry{
				UserID: f.String(),
				Online: sr.IsOnline(f),
				Status: sr.Get(f),
			})
		}
		return map[string]any{"following": out}, nil
	})
}

// PushPresence notifies all followers of a session's user about a
// presence change (connect / disconnect / status update).
func PushPresence(sr *status.Registry, registry *session.Registry, s *session.Session, online bool) {
	followers := sr.FollowersOf(s.UserID)
	var sessions []*session.Session
	for _, f := range followers {
		sessions = append(sessions, registry.ByUser(f)...)
	}
	SendToStream(sessions, "status.update", map[string]any{
		"username": s.Username,
		"online":   online,
		"status":   sr.Get(s.UserID),
	})
}
