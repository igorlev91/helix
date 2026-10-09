package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"helix/internal/session"
)

// Tournament ops
// Differences from leaderboards: time window (start_time..end_time) and
// max_attempts per player. Write merges by the tournament's operator.

type tournamentCreateRequest struct {
	ID          string `json:"id"`
	SortOrder   int    `json:"sort_order"`
	Operator    int    `json:"operator"`
	DurationSec int    `json:"duration_sec"`
	MaxAttempts int    `json:"max_attempts"`
}

type tournamentWriteRequest struct {
	ID    string `json:"id"`
	Score int64  `json:"score"`
}

type tournamentListRequest struct {
	ID    string `json:"id"`
	Limit int    `json:"limit"`
}

// RegisterTournamentOps installs tournament.create / write / list.
func (r *Router) RegisterTournamentOps(db *sql.DB) {
	ctx := context.Background()

	r.Register("tournament.create", func(s *session.Session, data json.RawMessage) (any, error) {
		var req tournamentCreateRequest
		if err := json.Unmarshal(data, &req); err != nil || req.ID == "" {
			return nil, errors.New("id required")
		}
		if req.DurationSec <= 0 {
			req.DurationSec = 3600
		}
		if req.MaxAttempts <= 0 {
			req.MaxAttempts = 5
		}
		_, err := db.ExecContext(ctx,
			`INSERT INTO tournaments (id, sort_order, operator, duration_sec, max_attempts, end_time)
			 VALUES ($1, $2, $3, $4::int, $5, now() + make_interval(secs => $4::int))
			 ON CONFLICT (id) DO NOTHING`,
			req.ID, req.SortOrder, req.Operator, req.DurationSec, req.MaxAttempts)
		if err != nil {
			return nil, err
		}
		return map[string]any{"created": req.ID, "duration_sec": req.DurationSec}, nil
	})

	// tournament.write — submit a score within the active window
	r.Register("tournament.write", func(s *session.Session, data json.RawMessage) (any, error) {
		var req tournamentWriteRequest
		if err := json.Unmarshal(data, &req); err != nil || req.ID == "" {
			return nil, errors.New("id required")
		}
		var sortOrder, operator, maxAttempts int
		var active bool
		err := db.QueryRowContext(ctx,
			`SELECT sort_order, operator, max_attempts,
			        (now() BETWEEN start_time AND end_time) AS active
			   FROM tournaments WHERE id = $1`, req.ID,
		).Scan(&sortOrder, &operator, &maxAttempts, &active)
		if err == sql.ErrNoRows {
			return nil, errors.New("tournament not found")
		}
		if err != nil {
			return nil, err
		}
		if !active {
			return nil, errors.New("tournament is not active")
		}

		// Attempt limit 
		var attempts int
		_ = db.QueryRowContext(ctx,
			`SELECT num_attempts FROM tournament_records
			  WHERE tournament_id = $1 AND owner_id = $2`, req.ID, s.UserID).Scan(&attempts)
		if attempts >= maxAttempts {
			return nil, errors.New("no attempts left")
		}

		var query string
		switch operator {
		case opSet:
			query = `INSERT INTO tournament_records (tournament_id, owner_id, username, score)
			         VALUES ($1, $2, $3, $4)
			         ON CONFLICT (tournament_id, owner_id)
			         DO UPDATE SET score = $4, username = $3, update_time = now(),
			                       num_attempts = tournament_records.num_attempts + 1`
		case opIncr:
			query = `INSERT INTO tournament_records (tournament_id, owner_id, username, score)
			         VALUES ($1, $2, $3, $4)
			         ON CONFLICT (tournament_id, owner_id)
			         DO UPDATE SET score = tournament_records.score + $4, username = $3, update_time = now(),
			                       num_attempts = tournament_records.num_attempts + 1`
		default: // opBest
			cmp := "<"
			if sortOrder == 0 {
				cmp = ">"
			}
			query = `INSERT INTO tournament_records (tournament_id, owner_id, username, score)
			         VALUES ($1, $2, $3, $4)
			         ON CONFLICT (tournament_id, owner_id)
			         DO UPDATE SET score = $4, username = $3, update_time = now(),
			                       num_attempts = tournament_records.num_attempts + 1
			         WHERE tournament_records.score ` + cmp + ` $4`
		}
		if _, err := db.ExecContext(ctx, query, req.ID, s.UserID, s.Username, req.Score); err != nil {
			return nil, err
		}

		var score int64
		_ = db.QueryRowContext(ctx,
			`SELECT score FROM tournament_records WHERE tournament_id = $1 AND owner_id = $2`,
			req.ID, s.UserID).Scan(&score)
		return map[string]any{"tournament": req.ID, "score": score, "attempts_left": maxAttempts - attempts - 1}, nil
	})

	// tournament.list — standings with ranks 
	r.Register("tournament.list", func(s *session.Session, data json.RawMessage) (any, error) {
		var req tournamentListRequest
		if err := json.Unmarshal(data, &req); err != nil || req.ID == "" {
			return nil, errors.New("id required")
		}
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 10
		}
		var sortOrder int
		if err := db.QueryRowContext(ctx,
			`SELECT sort_order FROM tournaments WHERE id = $1`, req.ID,
		).Scan(&sortOrder); err == sql.ErrNoRows {
			return nil, errors.New("tournament not found")
		} else if err != nil {
			return nil, err
		}
		dir := "DESC"
		if sortOrder == 0 {
			dir = "ASC"
		}
		rows, err := db.QueryContext(ctx,
			`SELECT username, score, num_attempts,
			        RANK() OVER (ORDER BY score `+dir+`) AS rank
			   FROM tournament_records WHERE tournament_id = $1
			   ORDER BY score `+dir+` LIMIT $2`, req.ID, req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type record struct {
			Username string `json:"username"`
			Score    int64  `json:"score"`
			Attempts int    `json:"attempts"`
			Rank     int    `json:"rank"`
		}
		out := []record{}
		for rows.Next() {
			var rec record
			if err := rows.Scan(&rec.Username, &rec.Score, &rec.Attempts, &rec.Rank); err != nil {
				return nil, err
			}
			out = append(out, rec)
		}
		return map[string]any{"tournament": req.ID, "records": out}, nil
	})
}
