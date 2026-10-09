package router

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"helix/internal/notify"
	"helix/internal/session"
)

// Notification ops
// In production, notification.send is a server-side call (runtime code);
// exposing it as an op here keeps it testable.

type notifySendRequest struct {
	UserID     string          `json:"user_id"`
	Subject    string          `json:"subject"`
	Code       int             `json:"code"`
	Content    json.RawMessage `json:"content"`
	Persistent bool            `json:"persistent"`
}

type notifyListRequest struct {
	Limit int `json:"limit"`
}

type notifyDeleteRequest struct {
	ID int64 `json:"id"`
}

func (r *Router) RegisterNotifyOps(n *notify.Notifier) {
	ctx := context.Background()

	r.Register("notification.send", func(s *session.Session, data json.RawMessage) (any, error) {
		var req notifySendRequest
		if err := json.Unmarshal(data, &req); err != nil || req.UserID == "" || req.Subject == "" {
			return nil, errors.New("user_id and subject required")
		}
		target, err := uuid.Parse(req.UserID)
		if err != nil {
			return nil, errors.New("invalid user_id")
		}
		if len(req.Content) == 0 {
			req.Content = json.RawMessage(`{}`)
		}
		if err := n.Send(ctx, target, req.Subject, req.Code, req.Content, req.Persistent, &s.UserID); err != nil {
			return nil, err
		}
		return map[string]bool{"sent": true}, nil
	})

	r.Register("notification.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req notifyListRequest
		_ = json.Unmarshal(data, &req) // empty body is fine
		items, err := n.List(ctx, s.UserID, req.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"notifications": items}, nil
	})

	r.Register("notification.delete", func(s *session.Session, data json.RawMessage) (any, error) {
		var req notifyDeleteRequest
		if err := json.Unmarshal(data, &req); err != nil || req.ID == 0 {
			return nil, errors.New("id required")
		}
		if err := n.Delete(ctx, s.UserID, req.ID); err != nil {
			return nil, err
		}
		return map[string]bool{"deleted": true}, nil
	})
}
