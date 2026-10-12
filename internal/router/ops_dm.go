package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"helix/internal/session"
	"helix/internal/tracker"
)

// Direct message ops
// A DM channel name is deterministic: "dm:<uidA>:<uidB>" with the two
// user IDs sorted, so both users land on the same stream regardless of
// who opened the conversation.

func dmChannel(a, b uuid.UUID) string {
	pair := [2]string{a.String(), b.String()}
	if pair[0] > pair[1] {
		pair[0], pair[1] = pair[1], pair[0]
	}
	return fmt.Sprintf("dm:%s:%s", pair[0], pair[1])
}

type dmOpenRequest struct {
	Username string `json:"username"`
}

type dmListRequest struct {
	Limit int `json:"limit"`
}

// RegisterDmOps installs dm.open / dm.list. Sending uses the regular
// chat.send op (DM streams are "chat:dm:<...>" under the hood).
func (r *Router) RegisterDmOps(db *sql.DB, registry *session.Registry, tr *tracker.Tracker) {
	ctx := context.Background()

	// dm.open — open (or rejoin) a conversation with a user.
	// Both parties' online sessions join the stream immediately
	r.Register("dm.open", func(s *session.Session, data json.RawMessage) (any, error) {
		var req dmOpenRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Username == "" {
			return nil, errors.New("username required")
		}
		var target uuid.UUID
		if err := db.QueryRowContext(ctx,
			`SELECT id FROM users WHERE username = $1`, req.Username,
		).Scan(&target); err == sql.ErrNoRows {
			return nil, errors.New("user not found")
		} else if err != nil {
			return nil, err
		}
		if target == s.UserID {
			return nil, errors.New("cannot DM yourself")
		}

		channel := dmChannel(s.UserID, target)
		stream := "chat:" + channel

		// Join all online sessions of both users.
		for _, sess := range registry.ByUser(s.UserID) {
			tr.Join(stream, sess)
		}
		var targetName string
		_ = db.QueryRowContext(ctx,
			`SELECT username FROM users WHERE id = $1`, target).Scan(&targetName)
		targetSessions := registry.ByUser(target)
		for _, sess := range targetSessions {
			tr.Join(stream, sess)
		}

		// Notify the other party that a conversation was opened.
		SendToStream(targetSessions, "dm.opened", map[string]any{
			"channel": channel,
			"from":    s.Username,
		})
		return map[string]any{"channel": channel, "with": targetName}, nil
	})

	// dm.list — my recent conversations with the last message preview
	r.Register("dm.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req dmListRequest
		_ = json.Unmarshal(data, &req)
		if req.Limit <= 0 || req.Limit > 50 {
			req.Limit = 20
		}
		// DM channels I'm part of: "dm:<me>:<other>" or "dm:<other>:<me>".
		rows, err := db.QueryContext(ctx, `
			SELECT m.channel, m.text, u.username, m.created_at
			  FROM chat_messages m
			  JOIN users u ON u.id = m.user_id
			 WHERE m.id IN (
			       SELECT MAX(id) FROM chat_messages
			        WHERE channel LIKE 'dm:%'
			          AND channel LIKE $1
			        GROUP BY channel)
			 ORDER BY m.id DESC LIMIT $2`,
			"%"+s.UserID.String()+"%", req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type conv struct {
			Channel    string `json:"channel"`
			LastText   string `json:"last_text"`
			LastSender string `json:"last_sender"`
			At         string `json:"at"`
		}
		out := []conv{}
		for rows.Next() {
			var c conv
			if err := rows.Scan(&c.Channel, &c.LastText, &c.LastSender, &c.At); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
		return map[string]any{"conversations": out}, nil
	})
}
