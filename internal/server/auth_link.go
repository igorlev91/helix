package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"helix/internal/auth"
)

// Link/unlink, attach or detach an external identity (device, email, apple, google) to the
// authenticated user. Requests carry the session JWT as Bearer.

// sessionUser validates the Authorization: Bearer header and returns the user id.
func sessionUser(r *http.Request, cache *auth.SessionCache, secret string) (uuid.UUID, bool) {
	header := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if header == "" {
		return uuid.Nil, false
	}
	claims, ok := cache.Parse(secret, header)
	if !ok {
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(claims.UserID)
	return userID, err == nil
}

type linkRequest struct {
	DeviceID string `json:"device_id,omitempty"`
	Email    string `json:"email,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"` // provider ID token
}

// NewAuthLinkHandler builds /auth/link/<provider> and /auth/unlink/<provider>.
// appleJWKS/audience are plumbed through for OIDC identity verification.
func NewAuthLinkHandler(db *sql.DB, secret string, sessionCache *auth.SessionCache,
	appleJWKS *auth.JWKSClient, appleAudience string,
	googleJWKS *auth.JWKSClient, googleAudience string) http.HandlerFunc {

	// verifyProviderToken turns a provider ID token into its external sub.
	verifyProviderToken := func(provider, token string) (string, bool) {
		var jwks *auth.JWKSClient
		var issuer, audience string
		switch provider {
		case "google":
			jwks, issuer, audience = googleJWKS, "https://accounts.google.com", googleAudience
		case "apple":
			jwks, issuer, audience = appleJWKS, "https://appleid.apple.com", appleAudience
		default:
			return "", false
		}
		if audience == "" {
			return "", false
		}
		claims, err := auth.VerifyOIDCToken(jwks, issuer, audience, token)
		if err != nil {
			return "", false
		}
		return claims.Subject, true
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/") // auth/link/<provider>
		if len(parts) != 3 || (parts[1] != "link" && parts[1] != "unlink") {
			http.Error(w, "unknown path", http.StatusNotFound)
			return
		}
		unlink := parts[1] == "unlink"
		userID, ok := sessionUser(r, sessionCache, secret)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := context.Background()

		// apply executes a link/unlink statement, translating unique
		// violations (identity already attached elsewhere) into 409.
		apply := func(query string, args ...any) (string, int) {
			_, err := db.ExecContext(ctx, query, args...)
			if err == nil {
				return "", 0
			}
			if isUniqueViolation(err) {
				return "identity already linked to another account", http.StatusConflict
			}
			return "internal error", http.StatusInternalServerError
		}

		var body string
		var code int
		var req linkRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch parts[2] {
		case "device":
			if unlink {
				body, code = apply(`UPDATE users SET device_id = NULL WHERE id = $1`, userID)
			} else if req.DeviceID == "" {
				body, code = "device_id required", http.StatusBadRequest
			} else {
				body, code = apply(`UPDATE users SET device_id = $2 WHERE id = $1`, userID, req.DeviceID)
			}
		case "email":
			if unlink {
				body, code = apply(`UPDATE users SET email = NULL, password_hash = NULL WHERE id = $1`, userID)
			} else if !validEmail(req.Email) || len(req.Password) < 6 {
				body, code = "valid email and password (6+) required", http.StatusBadRequest
			} else {
				sum, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
				if err != nil {
					body, code = "internal error", http.StatusInternalServerError
				} else {
					body, code = apply(`UPDATE users SET email = $2, password_hash = $3 WHERE id = $1`, userID, req.Email, string(sum))
				}
			}
		case "google", "apple":
			idColumn := parts[2] + "_id"
			if unlink {
				body, code = apply(`UPDATE users SET `+idColumn+` = NULL WHERE id = $1`, userID)
			} else {
				sub, verified := verifyProviderToken(parts[2], req.Token)
				if !verified {
					body, code = "invalid provider token", http.StatusUnauthorized
				} else {
					body, code = apply(`UPDATE users SET `+idColumn+` = $2 WHERE id = $1`, userID, sub)
				}
			}
		default:
			body, code = "unknown provider", http.StatusNotFound
		}

		if body != "" {
			http.Error(w, body, code)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
