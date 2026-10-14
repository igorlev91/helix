package server

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"helix/internal/auth"
)

// Account management
//   - GET  /account          — own profile
//   - POST /account/update   — username/display_name/avatar_url/metadata
//   - POST /account/password — change password (requires current one)
// All endpoints require the session JWT as Bearer.

type account struct {
	UserID      string          `json:"user_id"`
	Username    string          `json:"username"`
	Email       *string         `json:"email"`
	DisplayName *string         `json:"display_name"`
	AvatarURL   *string         `json:"avatar_url"`
	Metadata    json.RawMessage `json:"metadata"`
	CreatedAt   string          `json:"created_at"`
}

func NewAccountHandler(db *sql.DB, cache *auth.SessionCache, secret string) http.HandlerFunc {
	writeAccount := func(w http.ResponseWriter, userID uuid.UUID) {
		var a account
		var email, display, avatar sql.NullString
		err := db.QueryRow(
			`SELECT username, email, display_name, avatar_url, metadata, created_at
			   FROM users WHERE id = $1`, userID,
		).Scan(&a.Username, &email, &display, &avatar, &a.Metadata, &a.CreatedAt)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		a.UserID = userID.String()
		if email.Valid {
			a.Email = &email.String
		}
		if display.Valid {
			a.DisplayName = &display.String
		}
		if avatar.Valid {
			a.AvatarURL = &avatar.String
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(a)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := sessionUser(r, cache, secret)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/account":
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeAccount(w, userID)
		case "/account/update":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			// Partial update: only supplied fields change.
			var req struct {
				Username    *string          `json:"username"`
				DisplayName *string          `json:"display_name"`
				AvatarURL   *string          `json:"avatar_url"`
				Metadata    *json.RawMessage `json:"metadata"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if req.Username != nil {
				if len(*req.Username) < 3 {
					http.Error(w, "username must be at least 3 characters", http.StatusBadRequest)
					return
				}
				if _, err := db.Exec(`UPDATE users SET username = $2 WHERE id = $1`, userID, *req.Username); err != nil {
					if isUniqueViolation(err) {
						http.Error(w, "username taken", http.StatusConflict)
						return
					}
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}
			if req.DisplayName != nil {
				_, _ = db.Exec(`UPDATE users SET display_name = $2 WHERE id = $1`, userID, *req.DisplayName)
			}
			if req.AvatarURL != nil {
				_, _ = db.Exec(`UPDATE users SET avatar_url = $2 WHERE id = $1`, userID, *req.AvatarURL)
			}
			if req.Metadata != nil {
				if _, err := db.Exec(`UPDATE users SET metadata = $2 WHERE id = $1`, userID, string(*req.Metadata)); err != nil {
					http.Error(w, "metadata must be a JSON object", http.StatusBadRequest)
					return
				}
			}
			// Cached session claims still carry the old username; drop them.
			cache.EvictUser(userID.String())
			writeAccount(w, userID)
		case "/account/password":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			var req struct {
				Current string `json:"current_password"`
				New     string `json:"new_password"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Current == "" || len(req.New) < 6 {
				http.Error(w, "current_password and new_password (6+) required", http.StatusBadRequest)
				return
			}
			var hash sql.NullString
			if err := db.QueryRow(`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash); err != nil || !hash.Valid {
				http.Error(w, "no password set for this account", http.StatusBadRequest)
				return
			}
			if bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(req.Current)) != nil {
				http.Error(w, "current password incorrect", http.StatusForbidden)
				return
			}
			sum, err := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if _, err := db.Exec(`UPDATE users SET password_hash = $2 WHERE id = $1`, userID, string(sum)); err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			cache.EvictUser(userID.String()) // sessions keep working via re-parse
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}
}
