package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"helix/internal/auth"
)

// POST /auth/device: authenticate (or create) a user by device id and return
// a session token. 
func NewAuthDeviceHandler(db *sql.DB, secret string, tokenTTL time.Duration) http.HandlerFunc {
	type request struct {
		DeviceID string `json:"device_id"`
		Username string `json:"username,omitempty"`
		Create   *bool  `json:"create,omitempty"`
	}
	type response struct {
		Token    string `json:"token"`
		UserID   string `json:"user_id"`
		Username string `json:"username"`
		Created  bool   `json:"created"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" {
			http.Error(w, "device_id required", http.StatusBadRequest)
			return
		}
		create := req.Create == nil || *req.Create

		ctx := context.Background()
		var userID uuid.UUID
		var username string
		err := db.QueryRowContext(ctx,
			`SELECT id, username FROM users WHERE device_id = $1`, req.DeviceID,
		).Scan(&userID, &username)

		created := false
		if err == sql.ErrNoRows {
			if !create {
				http.Error(w, "user not found", http.StatusUnauthorized)
				return
			}
			// Create the account 
			userID = uuid.New()
			username = req.Username
			if username == "" {
				username = randomUsername()
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO users (id, username, device_id) VALUES ($1, $2, $3)`,
				userID, username, req.DeviceID); err != nil {
				log.Printf("auth: create user failed: %v", err)
				http.Error(w, "could not create user", http.StatusInternalServerError)
				return
			}
			created = true
		} else if err != nil {
			log.Printf("auth: lookup failed: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		token, _, err := auth.Generate(secret, userID, username, tokenTTL)
		if err != nil {
			http.Error(w, "could not sign token", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response{
			Token:    token,
			UserID:   userID.String(),
			Username: username,
			Created:  created,
		})
	}
}


func randomUsername() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
