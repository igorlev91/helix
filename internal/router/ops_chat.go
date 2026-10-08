package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"helix/internal/session"
	"helix/internal/tracker"
)


// A "channel" maps to a stream named "chat:<channel>".
type chatJoinRequest struct {
	Channel string `json:"channel"`
}

type chatSendRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

type chatHistoryRequest struct {
	Channel string `json:"channel"`
	Limit   int    `json:"limit"`
}

// RegisterChatOps installs chat.join / chat.leave / chat.send / chat.history.
// db may be nil (in-memory only, no persistence).
func (r *Router) RegisterChatOps(tr *tracker.Tracker, db *sql.DB) {
	streamName := func(channel string) string { return "chat:" + channel }

	r.Register("chat.join", func(s *session.Session, data json.RawMessage) (any, error) {
		var req chatJoinRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		tr.Join(streamName(req.Channel), s)

		// Notify the stream about the new presence
		SendToStream(tr.List(streamName(req.Channel)), "chat.presence", map[string]any{
			"channel":    req.Channel,
			"session_id": s.ID.String(),
			"event":      "join",
		})
		return map[string]string{"joined": req.Channel}, nil
	})

	r.Register("chat.leave", func(s *session.Session, data json.RawMessage) (any, error) {
		var req chatJoinRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		tr.Leave(streamName(req.Channel), s)
		SendToStream(tr.List(streamName(req.Channel)), "chat.presence", map[string]any{
			"channel":    req.Channel,
			"session_id": s.ID.String(),
			"event":      "leave",
		})
		return map[string]string{"left": req.Channel}, nil
	})

	r.Register("chat.send", func(s *session.Session, data json.RawMessage) (any, error) {
		var req chatSendRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		stream := streamName(req.Channel)
		// Sender must be in the channel to post to it.
		for _, member := range tr.List(stream) {
			if member.ID == s.ID {
				// Persist before broadcasting 
				if db != nil {
					if _, err := db.ExecContext(context.Background(),
						`INSERT INTO chat_messages (channel, session_id, text) VALUES ($1, $2, $3)`,
						req.Channel, s.ID, req.Text); err != nil {
						return nil, err
					}
				}
				SendToStream(tr.List(stream), "chat.message", map[string]any{
					"channel":    req.Channel,
					"session_id": s.ID.String(),
					"text":       req.Text,
				})
				return map[string]string{"sent": req.Channel}, nil
			}
		}
		return nil, errors.New("not in channel")
	})

	// chat.history -> last N messages of a channel
	r.Register("chat.history", func(s *session.Session, data json.RawMessage) (any, error) {
		if db == nil {
			return nil, errors.New("persistence disabled")
		}
		var req chatHistoryRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 20
		}
		rows, err := db.QueryContext(context.Background(),
			`SELECT id, session_id, text, created_at
			   FROM chat_messages
			  WHERE channel = $1
			  ORDER BY id DESC
			  LIMIT $2`, req.Channel, req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type msg struct {
			ID        int64  `json:"id"`
			SessionID string `json:"session_id"`
			Text      string `json:"text"`
			CreatedAt string `json:"created_at"`
		}
		out := []msg{}
		for rows.Next() {
			var m msg
			if err := rows.Scan(&m.ID, &m.SessionID, &m.Text, &m.CreatedAt); err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return map[string]any{"channel": req.Channel, "messages": out}, nil
	})
}
