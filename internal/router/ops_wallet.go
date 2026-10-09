package router

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Wallet ops
// The wallet is an append-only ledger: balance = SUM of changesets.
// Client-facing ops are intentionally limited: credit/debit with a negative
// balance check. In production games, credit is usually server-only

type walletChangeRequest struct {
	Amount int64  `json:"amount"` // signed: +credit / -debit
	Reason string `json:"reason"`
}

type walletHistoryRequest struct {
	Limit int `json:"limit"`
}

// RegisterWalletOps installs wallet.get / wallet.change / wallet.history.
func (r *Router) RegisterWalletOps(db *sql.DB) {
	ctx := context.Background()

	balance := func(userID uuid.UUID) (int64, error) {
		var sum sql.NullInt64
		err := db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM((changeset->>'coins')::bigint), 0)
			   FROM wallet_ledger WHERE user_id = $1`, userID).Scan(&sum)
		return sum.Int64, err
	}

	// wallet.get — current balance
	r.Register("wallet.get", func(s *session.Session, data json.RawMessage) (any, error) {
		b, err := balance(s.UserID)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"coins": b}, nil
	})

	// wallet.change — apply a signed changeset. Debits that would take the
	// balance below zero are rejected 
	r.Register("wallet.change", func(s *session.Session, data json.RawMessage) (any, error) {
		var req walletChangeRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Amount == 0 {
			return nil, errors.New("non-zero amount required")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()

		if req.Amount < 0 {
			var sum int64
			if err := tx.QueryRowContext(ctx,
				`SELECT COALESCE(SUM((changeset->>'coins')::bigint), 0)
				   FROM wallet_ledger WHERE user_id = $1`, s.UserID).Scan(&sum); err != nil {
				return nil, err
			}
			if sum+req.Amount < 0 {
				return nil, errors.New("insufficient funds")
			}
		}

		changeset, _ := json.Marshal(map[string]int64{"coins": req.Amount})
		metadata, _ := json.Marshal(map[string]string{"reason": req.Reason})
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO wallet_ledger (id, user_id, changeset, metadata) VALUES ($1, $2, $3, $4)`,
			uuid.New(), s.UserID, changeset, metadata); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		b, _ := balance(s.UserID)
		return map[string]int64{"coins": b}, nil
	})

	// wallet.history — audit trail 
	r.Register("wallet.history", func(s *session.Session, data json.RawMessage) (any, error) {
		var req walletHistoryRequest
		_ = json.Unmarshal(data, &req)
		if req.Limit <= 0 || req.Limit > 100 {
			req.Limit = 20
		}
		rows, err := db.QueryContext(ctx,
			`SELECT changeset, metadata, create_time FROM wallet_ledger
			  WHERE user_id = $1 ORDER BY create_time DESC, id DESC LIMIT $2`,
			s.UserID, req.Limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		type entry struct {
			Changeset  json.RawMessage `json:"changeset"`
			Metadata   json.RawMessage `json:"metadata"`
			CreateTime string          `json:"create_time"`
		}
		out := []entry{}
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.Changeset, &e.Metadata, &e.CreateTime); err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return map[string]any{"entries": out}, nil
	})
}
