package router

import (
	"encoding/json"

	"helix/internal/session"
)

// RegisterBuiltinOps installs the built-in ops.
func (r *Router) RegisterBuiltinOps(registry *session.Registry) {
	r.Register("ping", func(s *session.Session, data json.RawMessage) (any, error) {
		return map[string]string{"pong": "ok"}, nil
	})

	// echo -> returns the payload back (test op)
	r.Register("echo", func(s *session.Session, data json.RawMessage) (any, error) {
		return data, nil
	})

	// who -> own session id, username and the online count
	r.Register("who", func(s *session.Session, data json.RawMessage) (any, error) {
		return map[string]any{
			"session_id": s.ID.String(),
			"user_id":    s.UserID.String(),
			"username":   s.Username,
			"online":     registry.Count(),
		}, nil
	})
}
