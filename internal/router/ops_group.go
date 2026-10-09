package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Group (clan) ops
// Group state: 0 = open, 1 = closed. Membership state:
// 0 = superadmin (creator), 1 = admin, 2 = member, 3 = join_request.
const (
	groupOpen   = 0
	groupClosed = 1

	memberSuperAdmin  = 0
	memberAdmin       = 1
	memberMember      = 2
	memberJoinRequest = 3
)

type groupCreateRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Open        *bool  `json:"open"` // default true
	MaxCount    int    `json:"max_count"`
}

type groupIDRequest struct {
	GroupID string `json:"group_id"`
}

// RegisterGroupOps installs group.create / join / leave / members / list.
func (r *Router) RegisterGroupOps(db *sql.DB) {
	ctx := context.Background()

	// group.create — creator becomes superadmin (Nakama: CreateGroup).
	r.Register("group.create", func(s *session.Session, data json.RawMessage) (any, error) {
		var req groupCreateRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Name == "" {
			return nil, errors.New("name required")
		}
		open := req.Open == nil || *req.Open
		if req.MaxCount <= 0 {
			req.MaxCount = 100
		}
		groupID := uuid.New()
		state := groupClosed
		if open {
			state = groupOpen
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO groups (id, creator_id, name, description, state, max_count)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			groupID, s.UserID, req.Name, req.Description, state, req.MaxCount); err != nil {
			tx.Rollback()
			return nil, errors.New("group name taken")
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO group_users (group_id, user_id, state) VALUES ($1, $2, 0)`,
			groupID, s.UserID); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return map[string]any{"group_id": groupID.String(), "name": req.Name}, nil
	})

	// group.join — open group: member instantly; closed: join_request
	r.Register("group.join", func(s *session.Session, data json.RawMessage) (any, error) {
		var req groupIDRequest
		if err := json.Unmarshal(data, &req); err != nil || req.GroupID == "" {
			return nil, errors.New("group_id required")
		}
		gid, err := uuid.Parse(req.GroupID)
		if err != nil {
			return nil, errors.New("invalid group_id")
		}
		var state, edgeCount, maxCount int
		if err := db.QueryRowContext(ctx,
			`SELECT state, edge_count, max_count FROM groups WHERE id = $1`, gid,
		).Scan(&state, &edgeCount, &maxCount); err == sql.ErrNoRows {
			return nil, errors.New("group not found")
		} else if err != nil {
			return nil, err
		}
		if edgeCount >= maxCount {
			return nil, errors.New("group is full")
		}

		memberState := memberJoinRequest
		if state == groupOpen {
			memberState = memberMember
		}
		res, err := db.ExecContext(ctx,
			`INSERT INTO group_users (group_id, user_id, state) VALUES ($1, $2, $3)
			 ON CONFLICT (group_id, user_id) DO NOTHING`, gid, s.UserID, memberState)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, errors.New("already in group or join requested")
		}
		if memberState == memberMember {
			_, _ = db.ExecContext(ctx,
				`UPDATE groups SET edge_count = edge_count + 1, update_time = now() WHERE id = $1`, gid)
		}
		return map[string]any{"group_id": req.GroupID, "state": memberState}, nil
	})

	// group.accept — admin approves a join request
	r.Register("group.accept", func(s *session.Session, data json.RawMessage) (any, error) {
		var req struct {
			GroupID  string `json:"group_id"`
			Username string `json:"username"`
		}
		if err := json.Unmarshal(data, &req); err != nil || req.GroupID == "" || req.Username == "" {
			return nil, errors.New("group_id and username required")
		}
		gid, err := uuid.Parse(req.GroupID)
		if err != nil {
			return nil, errors.New("invalid group_id")
		}
		// Caller must be admin or superadmin.
		var myState int
		if err := db.QueryRowContext(ctx,
			`SELECT state FROM group_users WHERE group_id = $1 AND user_id = $2`,
			gid, s.UserID).Scan(&myState); err != nil || myState > memberAdmin {
			return nil, errors.New("admin rights required")
		}
		res, err := db.ExecContext(ctx, `
			UPDATE group_users SET state = 2, update_time = now()
			 WHERE group_id = $1 AND state = 3
			   AND user_id = (SELECT id FROM users WHERE username = $2)`, gid, req.Username)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, errors.New("no pending join request")
		}
		_, _ = db.ExecContext(ctx,
			`UPDATE groups SET edge_count = edge_count + 1, update_time = now() WHERE id = $1`, gid)
		return map[string]any{"accepted": req.Username}, nil
	})

	// group.leave — leave a group; superadmin leaving closes it
	r.Register("group.leave", func(s *session.Session, data json.RawMessage) (any, error) {
		var req groupIDRequest
		if err := json.Unmarshal(data, &req); err != nil || req.GroupID == "" {
			return nil, errors.New("group_id required")
		}
		gid, err := uuid.Parse(req.GroupID)
		if err != nil {
			return nil, errors.New("invalid group_id")
		}
		var myState int
		if err := db.QueryRowContext(ctx,
			`SELECT state FROM group_users WHERE group_id = $1 AND user_id = $2`,
			gid, s.UserID).Scan(&myState); err != nil {
			return nil, errors.New("not in group")
		}
		if myState == memberSuperAdmin {
			// Nakama: superadmin leaving deletes the group.
			if _, err := db.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, gid); err != nil {
				return nil, err
			}
			return map[string]any{"left": req.GroupID, "group_deleted": true}, nil
		}
		if _, err := db.ExecContext(ctx,
			`DELETE FROM group_users WHERE group_id = $1 AND user_id = $2`, gid, s.UserID); err != nil {
			return nil, err
		}
		if myState <= memberMember {
			_, _ = db.ExecContext(ctx,
				`UPDATE groups SET edge_count = edge_count - 1, update_time = now() WHERE id = $1`, gid)
		}
		return map[string]any{"left": req.GroupID, "group_deleted": false}, nil
	})

	// group.members — member list with states.
	r.Register("group.members", func(s *session.Session, data json.RawMessage) (any, error) {
		var req groupIDRequest
		if err := json.Unmarshal(data, &req); err != nil || req.GroupID == "" {
			return nil, errors.New("group_id required")
		}
		gid, err := uuid.Parse(req.GroupID)
		if err != nil {
			return nil, errors.New("invalid group_id")
		}
		rows, err := db.QueryContext(ctx, `
			SELECT u.username, g.state FROM group_users g
			  JOIN users u ON u.id = g.user_id
			 WHERE g.group_id = $1 ORDER BY g.state, g.update_time`, gid)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type member struct {
			Username string `json:"username"`
			State    int    `json:"state"`
		}
		out := []member{}
		for rows.Next() {
			var m member
			if err := rows.Scan(&m.Username, &m.State); err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return map[string]any{"members": out}, nil
	})

	// group.list — search groups by name
	r.Register("group.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req struct {
			Name  string `json:"name"` // substring filter, empty = all
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(data, &req)
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 20
		}
		rows, err := db.QueryContext(ctx, `
			SELECT id, name, state, edge_count, max_count FROM groups
			 WHERE name ILIKE '%' || $1 || '%'
			 ORDER BY edge_count DESC LIMIT $2`, req.Name, req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type group struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			State    int    `json:"state"`
			Members  int    `json:"members"`
			MaxCount int    `json:"max_count"`
		}
		out := []group{}
		for rows.Next() {
			var g group
			if err := rows.Scan(&g.ID, &g.Name, &g.State, &g.Members, &g.MaxCount); err != nil {
				return nil, err
			}
			out = append(out, g)
		}
		return map[string]any{"groups": out}, nil
	})
}
