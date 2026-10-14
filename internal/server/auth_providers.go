package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"helix/internal/auth"
)

// isUniqueViolation reports a Postgres unique constraint violation
// (SQLSTATE 23505), which pgx surfaces in the error message.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

// validEmail is a pragmatic format check (no DNS validation).
func validEmail(email string) bool {
	at := -1
	for i, ch := range email {
		if ch == '@' {
			if at != -1 {
				return false
			}
			at = i
		}
	}
	if at <= 0 || at >= len(email)-3 {
		return false
	}
	return strings.Contains(email[at:], ".")
}

// Auth providers, mirroring Nakama's api_authenticate.go:
//   - /auth/email  — register/login with email+password (bcrypt)
//   - /auth/google — Google ID token (OIDC, verified against Google JWKS)
//   - /auth/apple  — Apple identity token (OIDC, verified against Apple JWKS)
// Every provider resolves to the same users row and issues the same
// session token pair as /auth/device.

type authResponse struct {
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	Username     string `json:"username"`
	Created      bool   `json:"created"`
}

// writeSession issues the token pair and writes the shared response shape.
func writeSession(w http.ResponseWriter, secret, refreshSecret string,
	tokenTTL, refreshTTL time.Duration, userID uuid.UUID, username string, created bool) {
	token, _, err := auth.Generate(secret, userID, username, tokenTTL)
	if err != nil {
		http.Error(w, "could not sign token", http.StatusInternalServerError)
		return
	}
	refresh, err := auth.GenerateRefresh(refreshSecret, userID, username, refreshTTL)
	if err != nil {
		http.Error(w, "could not sign refresh token", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(authResponse{
		Token: token, RefreshToken: refresh,
		UserID: userID.String(), Username: username, Created: created,
	})
}

// POST /auth/email {email, password, username?, create?}.
func NewAuthEmailHandler(db *sql.DB, limiter *LoginAttemptLimiter, secret, refreshSecret string, tokenTTL, refreshTTL time.Duration) http.HandlerFunc {
	type request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Username string `json:"username,omitempty"`
		Create   *bool  `json:"create,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" {
			http.Error(w, "email and password required", http.StatusBadRequest)
			return
		}
		// Input validation: a well-formed email and a non-trivial password.
		if !validEmail(req.Email) {
			http.Error(w, "invalid email", http.StatusBadRequest)
			return
		}
		if len(req.Password) < 6 {
			http.Error(w, "password must be at least 6 characters", http.StatusBadRequest)
			return
		}
		// Brute-force protection (Nakama: login attempt cache).
		if remaining, locked := limiter.IsLocked(req.Email); locked {
			http.Error(w, "too many attempts, retry in "+remaining.Round(time.Second).String(), http.StatusTooManyRequests)
			return
		}
		create := req.Create == nil || *req.Create
		ctx := context.Background()

		var userID uuid.UUID
		var username string
		var hash sql.NullString
		err := db.QueryRowContext(ctx,
			`SELECT id, username, password_hash FROM users WHERE email = $1`, req.Email,
		).Scan(&userID, &username, &hash)

		created := false
		if err == sql.ErrNoRows {
			if !create {
				http.Error(w, "user not found", http.StatusUnauthorized)
				return
			}
			sum, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			userID = uuid.New()
			username = req.Username
			if username == "" {
				username = randomUsername()
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO users (id, username, email, password_hash) VALUES ($1, $2, $3, $4)`,
				userID, username, req.Email, string(sum)); err != nil {
				// Unique violation (email/username taken) vs a real error.
				if isUniqueViolation(err) {
					http.Error(w, "email or username already registered", http.StatusConflict)
					return
				}
				log.Printf("auth/email: create failed: %v", err)
				http.Error(w, "could not create user", http.StatusInternalServerError)
				return
			}
			created = true
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		} else {
			// Existing account: password must match
			if !hash.Valid || bcrypt.CompareHashAndPassword([]byte(hash.String), []byte(req.Password)) != nil {
				if lockFor := limiter.RecordFailure(req.Email); lockFor > 0 {
					http.Error(w, "too many attempts, retry in "+lockFor.Round(time.Second).String(), http.StatusTooManyRequests)
				} else {
					http.Error(w, "invalid credentials", http.StatusUnauthorized)
				}
				return
			}
			limiter.Reset(req.Email)
		}
		writeSession(w, secret, refreshSecret, tokenTTL, refreshTTL, userID, username, created)
	}
}

// NewAuthOIDCHandler builds /auth/google and /auth/apple: verify the
// provider ID token, then find-or-create a user by the external id column.
func NewAuthOIDCHandler(db *sql.DB, jwks *auth.JWKSClient, issuer, audience, idColumn string,
	secret, refreshSecret string, tokenTTL, refreshTTL time.Duration) http.HandlerFunc {
	type request struct {
		Token    string `json:"token"`
		Username string `json:"username,omitempty"`
		Create   *bool  `json:"create,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if audience == "" {
			http.Error(w, "provider not configured", http.StatusNotImplemented)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
			http.Error(w, "token required", http.StatusBadRequest)
			return
		}
		claims, err := auth.VerifyOIDCToken(jwks, issuer, audience, req.Token)
		if err != nil {
			log.Printf("auth/oidc(%s): verify failed: %v", idColumn, err)
			http.Error(w, "invalid provider token", http.StatusUnauthorized)
			return
		}
		create := req.Create == nil || *req.Create
		ctx := context.Background()

		var userID uuid.UUID
		var username string
		err = db.QueryRowContext(ctx,
			`SELECT id, username FROM users WHERE `+idColumn+` = $1`, claims.Subject,
		).Scan(&userID, &username)

		created := false
		if err == sql.ErrNoRows {
			if !create {
				http.Error(w, "user not found", http.StatusUnauthorized)
				return
			}
			userID = uuid.New()
			username = req.Username
			if username == "" {
				username = randomUsername()
			}
			if _, err := db.ExecContext(ctx,
				`INSERT INTO users (id, username, `+idColumn+`) VALUES ($1, $2, $3)`,
				userID, username, claims.Subject); err != nil {
				log.Printf("auth/oidc(%s): create failed: %v", idColumn, err)
				http.Error(w, "could not create user", http.StatusInternalServerError)
				return
			}
			created = true
		} else if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeSession(w, secret, refreshSecret, tokenTTL, refreshTTL, userID, username, created)
	}
}
