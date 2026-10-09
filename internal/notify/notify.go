package notify

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"

	"github.com/google/uuid"

	"helix/internal/session"
)

// Notifier delivers server-initiated notifications to users.
//   - persistent=true  -> stored in the notifications table AND pushed if online
//   - persistent=false -> pushed only, never stored
type Notifier struct {
	db       *sql.DB
	registry *session.Registry
}

func New(db *sql.DB, registry *session.Registry) *Notifier {
	return &Notifier{db: db, registry: registry}
}

// Send stores (optionally) and pushes a notification to all online
// sessions of the user 
func (n *Notifier) Send(ctx context.Context, userID uuid.UUID, subject string, code int, content any, persistent bool, senderID *uuid.UUID) error {
	raw, err := json.Marshal(content)
	if err != nil {
		return err
	}

	var id int64
	if persistent {
		err = n.db.QueryRowContext(ctx,
			`INSERT INTO notifications (user_id, subject, content, code, sender_id)
			 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			userID, subject, raw, code, senderID).Scan(&id)
		if err != nil {
			return err
		}
	}

	// Live push to every connected session of the user.
	payload, err := json.Marshal(map[string]any{
		"op": "notification",
		"data": map[string]any{
			"id":      id,
			"subject": subject,
			"code":    code,
			"content": raw,
		},
	})
	if err != nil {
		return err
	}
	for _, s := range n.registry.ByUser(userID) {
		s.Send(payload)
	}
	return nil
}

// List returns the user's persistent notifications, newest first
func (n *Notifier) List(ctx context.Context, userID uuid.UUID, limit int) ([]map[string]any, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := n.db.QueryContext(ctx,
		`SELECT id, subject, content, code, create_time
		   FROM notifications WHERE user_id = $1
		   ORDER BY id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var subject string
		var content json.RawMessage
		var code int
		var created string
		if err := rows.Scan(&id, &subject, &content, &code, &created); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{
			"id": id, "subject": subject, "content": content,
			"code": code, "create_time": created,
		})
	}
	return out, rows.Err()
}

// Delete removes one notification owned by the user
func (n *Notifier) Delete(ctx context.Context, userID uuid.UUID, id int64) error {
	res, err := n.db.ExecContext(ctx,
		`DELETE FROM notifications WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if n2, _ := res.RowsAffected(); n2 == 0 {
		log.Printf("notify: notification %d not found for user %s", id, userID)
	}
	return nil
}
