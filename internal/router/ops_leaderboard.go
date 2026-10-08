package router

import (
	"context"
	"database/sql"
	"errors"

	"encoding/json"

	"helix/internal/session"
)

// Leaderboard ops
// Operators: 0 best / 1 set / 2 incr / 3 decr. Sort: 0 asc / 1 desc.

const (
	opBest = 0
	opSet  = 1
	opIncr = 2
	opDecr = 3
)

type lbCreateRequest struct {
	ID        string `json:"id"`
	SortOrder int    `json:"sort_order"` // 0 asc, 1 desc (default)
	Operator  int    `json:"operator"`   // 0 best (default), 1 set, 2 incr, 3 decr
}

type lbWriteRequest struct {
	Board string `json:"board"`
	Score int64  `json:"score"`
}

type lbListRequest struct {
	Board string `json:"board"`
	Limit int    `json:"limit"`
}

// RegisterLeaderboardOps installs leaderboard.create / write / list / delete.
func (r *Router) RegisterLeaderboardOps(db *sql.DB) {
	ctx := context.Background()

	// leaderboard.create — define a board
	r.Register("leaderboard.create", func(s *session.Session, data json.RawMessage) (any, error) {
		var req lbCreateRequest
		if err := json.Unmarshal(data, &req); err != nil || req.ID == "" {
			return nil, errors.New("id required")
		}
		if req.SortOrder != 0 {
			req.SortOrder = 1
		}
		if req.Operator < 0 || req.Operator > 3 {
			return nil, errors.New("invalid operator")
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO leaderboards (id, sort_order, operator) VALUES ($1, $2, $3)
			 ON CONFLICT (id) DO NOTHING`, req.ID, req.SortOrder, req.Operator)
		if err != nil {
			return nil, err
		}
		return map[string]any{"created": req.ID, "sort_order": req.SortOrder, "operator": req.Operator}, nil
	})

	// leaderboard.write — submit a score; the board's operator decides how
	// it merges with the existing record
	r.Register("leaderboard.write", func(s *session.Session, data json.RawMessage) (any, error) {
		var req lbWriteRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Board == "" {
			return nil, errors.New("board required")
		}
		var sortOrder, operator int
		if err := db.QueryRowContext(ctx,
			`SELECT sort_order, operator FROM leaderboards WHERE id = $1`, req.Board,
		).Scan(&sortOrder, &operator); err == sql.ErrNoRows {
			return nil, errors.New("leaderboard not found")
		} else if err != nil {
			return nil, err
		}

		var query string
		switch operator {
		case opSet:
			query = `INSERT INTO leaderboard_records (leaderboard_id, owner_id, username, score, update_time)
			         VALUES ($1, $2, $3, $4, now())
			         ON CONFLICT (leaderboard_id, owner_id)
			         DO UPDATE SET score = $4, username = $3, update_time = now(), num_score = leaderboard_records.num_score + 1`
		case opIncr:
			query = `INSERT INTO leaderboard_records (leaderboard_id, owner_id, username, score, update_time)
			         VALUES ($1, $2, $3, $4, now())
			         ON CONFLICT (leaderboard_id, owner_id)
			         DO UPDATE SET score = leaderboard_records.score + $4, username = $3, update_time = now(), num_score = leaderboard_records.num_score + 1`
		case opDecr:
			query = `INSERT INTO leaderboard_records (leaderboard_id, owner_id, username, score, update_time)
			         VALUES ($1, $2, $3, $4, now())
			         ON CONFLICT (leaderboard_id, owner_id)
			         DO UPDATE SET score = leaderboard_records.score - $4, username = $3, update_time = now(), num_score = leaderboard_records.num_score + 1`
		default: // opBest: keep the better score depending on sort order
			cmp := "<" // desc board: update only when the new score beats the old one
			if sortOrder == 0 {
				cmp = ">" // asc board: lower score is better (e.g. race times)
			}
			query = `INSERT INTO leaderboard_records (leaderboard_id, owner_id, username, score, update_time)
			         VALUES ($1, $2, $3, $4, now())
			         ON CONFLICT (leaderboard_id, owner_id)
			         DO UPDATE SET score = $4, username = $3, update_time = now(), num_score = leaderboard_records.num_score + 1
			         WHERE leaderboard_records.score ` + cmp + ` $4`
			// Note: when the existing score is better, nothing updates (by design).
		}
		if _, err := db.ExecContext(ctx, query, req.Board, s.UserID, s.Username, req.Score); err != nil {
			return nil, err
		}

		var score int64
		_ = db.QueryRowContext(ctx,
			`SELECT score FROM leaderboard_records WHERE leaderboard_id = $1 AND owner_id = $2`,
			req.Board, s.UserID).Scan(&score)
		return map[string]any{"board": req.Board, "score": score}, nil
	})

	// leaderboard.list — top N with ranks 
	// Rank comes from a window function over the board's sort order.
	r.Register("leaderboard.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req lbListRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Board == "" {
			return nil, errors.New("board required")
		}
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 10
		}
		var sortOrder int
		if err := db.QueryRowContext(ctx,
			`SELECT sort_order FROM leaderboards WHERE id = $1`, req.Board,
		).Scan(&sortOrder); err == sql.ErrNoRows {
			return nil, errors.New("leaderboard not found")
		} else if err != nil {
			return nil, err
		}

		dir := "DESC"
		if sortOrder == 0 {
			dir = "ASC"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT username, score, num_score,
			        RANK() OVER (ORDER BY score `+dir+`) AS rank
			   FROM leaderboard_records
			  WHERE leaderboard_id = $1
			  ORDER BY score `+dir+`
			  LIMIT $2`, req.Board, req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type record struct {
			Username string `json:"username"`
			Score    int64  `json:"score"`
			NumScore int    `json:"num_score"`
			Rank     int    `json:"rank"`
		}
		out := []record{}
		for rows.Next() {
			var rec record
			if err := rows.Scan(&rec.Username, &rec.Score, &rec.NumScore, &rec.Rank); err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
		return map[string]any{"board": req.Board, "records": out}, nil
	})

	// leaderboard.delete — remove own record.
	r.Register("leaderboard.delete", func(s *session.Session, data json.RawMessage) (any, error) {
		var req lbWriteRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Board == "" {
			return nil, errors.New("board required")
		}
		res, err := db.ExecContext(ctx,
			`DELETE FROM leaderboard_records WHERE leaderboard_id = $1 AND owner_id = $2`,
			req.Board, s.UserID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, errors.New("record not found")
		}
		return map[string]string{"deleted": req.Board}, nil
	})
}
