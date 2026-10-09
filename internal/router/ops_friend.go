package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"helix/internal/notify"
	"helix/internal/session"
)

// Friend ops, simplified analog of core_friend.go in Nakama.
//	0 = friend (mutual), 1 = invite_sent, 2 = invite_received, 3 = blocked
const (
	stateFriend         = 0
	stateInviteSent     = 1
	stateInviteReceived = 2
	stateBlocked        = 3
)

type friendRequest struct {
	Username string `json:"username"` // target user by username
}

type friendListRequest struct {
	State int `json:"state"` // -1 = all
	Limit int `json:"limit"`
}

// RegisterFriendOps installs friend.add / friend.remove / friend.block / friend.list.
func (r *Router) RegisterFriendOps(db *sql.DB, registry *session.Registry, n *notify.Notifier) {
	ctx := context.Background()

	findUser := func(username string) (uuid.UUID, error) {
		var id uuid.UUID
		err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE username = $1`, username).Scan(&id)
		if err == sql.ErrNoRows {
			return id, errors.New("user not found")
		}
		return id, err
	}

	edgeState := func(src, dst uuid.UUID) (int, bool) {
		var st int
		err := db.QueryRowContext(ctx,
			`SELECT state FROM friends WHERE source_id = $1 AND destination_id = $2`,
			src, dst).Scan(&st)
		if err != nil {
			return 0, false
		}
		return st, true
	}

	// friend.add — send or accept an invite.
	r.Register("friend.add", func(s *session.Session, data json.RawMessage) (any, error) {
		var req friendRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Username == "" {
			return nil, errors.New("username required")
		}
		target, err := findUser(req.Username)
		if err != nil {
			return nil, err
		}
		if target == s.UserID {
			return nil, errors.New("cannot add yourself")
		}

		// Check the reverse edge first.
		if rev, ok := edgeState(target, s.UserID); ok {
			switch rev {
			case stateBlocked:
				return nil, errors.New("blocked")
			case stateInviteSent:
				// They invited us; adding back = accept. Both become friends.
				_, err := db.ExecContext(ctx, `
					UPDATE friends SET state = 0, update_time = now()
					 WHERE (source_id = $1 AND destination_id = $2)
					    OR (source_id = $2 AND destination_id = $1)`, s.UserID, target)
				if err != nil {
					return nil, err
				}
				_ = n.Send(ctx, target, "Friend request accepted", 20,
					map[string]string{"username": s.Username}, true, &s.UserID)
				return map[string]any{"state": stateFriend, "username": req.Username}, nil
			case stateFriend:
				return map[string]any{"state": stateFriend, "username": req.Username}, nil
			}
		}

		// No reverse edge (or they only received ours): create the invite pair.
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO friends (source_id, destination_id, state) VALUES ($1, $2, 1), ($2, $1, 2)
			ON CONFLICT (source_id, destination_id)
			DO UPDATE SET state = EXCLUDED.state, update_time = now()`, s.UserID, target)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		// Persistent notification: invitee sees it even when offline
		_ = n.Send(ctx, target, "Friend request", 20,
			map[string]string{"username": s.Username}, true, &s.UserID)
		return map[string]any{"state": stateInviteSent, "username": req.Username}, nil
	})

	// friend.remove — delete the relationship in both directions.
	r.Register("friend.remove", func(s *session.Session, data json.RawMessage) (any, error) {
		var req friendRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Username == "" {
			return nil, errors.New("username required")
		}
		target, err := findUser(req.Username)
		if err != nil {
			return nil, err
		}
		_, err = db.ExecContext(ctx, `
			DELETE FROM friends
			 WHERE (source_id = $1 AND destination_id = $2)
			    OR (source_id = $2 AND destination_id = $1)`, s.UserID, target)
		if err != nil {
			return nil, err
		}
		return map[string]any{"removed": req.Username}, nil
	})

	// friend.block — my edge becomes blocked, their edge is deleted
	r.Register("friend.block", func(s *session.Session, data json.RawMessage) (any, error) {
		var req friendRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Username == "" {
			return nil, errors.New("username required")
		}
		target, err := findUser(req.Username)
		if err != nil {
			return nil, err
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO friends (source_id, destination_id, state) VALUES ($1, $2, 3)
			ON CONFLICT (source_id, destination_id)
			DO UPDATE SET state = 3, update_time = now()`, s.UserID, target); err != nil {
			tx.Rollback()
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM friends WHERE source_id = $1 AND destination_id = $2`, target, s.UserID); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return map[string]any{"blocked": req.Username}, nil
	})

	// friend.list — my edges with live online status from the session registry
	r.Register("friend.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req friendListRequest
		req.State = -1
		_ = json.Unmarshal(data, &req)
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 50
		}
		query := `
			SELECT u.username, f.state, f.destination_id, f.update_time
			  FROM friends f JOIN users u ON u.id = f.destination_id
			 WHERE f.source_id = $1`
		args := []any{s.UserID}
		if req.State >= 0 {
			query += ` AND f.state = $2`
			args = append(args, req.State)
		}
		query += ` ORDER BY f.update_time DESC`
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type entry struct {
			Username string `json:"username"`
			State    int    `json:"state"`
			Online   bool   `json:"online"`
		}
		out := []entry{}
		for rows.Next() {
			var e entry
			var dest uuid.UUID
			var updated string
			if err := rows.Scan(&e.Username, &e.State, &dest, &updated); err != nil {
				return nil, err
			}
			e.Online = len(registry.ByUser(dest)) > 0
			out = append(out, e)
		}
		return map[string]any{"friends": out}, nil
	})
}
