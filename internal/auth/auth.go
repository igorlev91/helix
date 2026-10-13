package auth

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type TokenClaims struct {
	UserID   string `json:"uid"`
	Username string `json:"usn"`
	jwt.RegisteredClaims
}

// Generate signs a session token with HS256.
func Generate(secret string, userID uuid.UUID, username string, ttl time.Duration) (string, int64, error) {
	exp := time.Now().UTC().Add(ttl).Unix()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &TokenClaims{
		UserID:   userID.String(),
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(), // jti
			ExpiresAt: jwt.NewNumericDate(time.Unix(exp, 0)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
	})
	signed, err := token.SignedString([]byte(secret))
	return signed, exp, err
}

// Parse validates a token and returns its claims.
func Parse(secret, tokenString string) (*TokenClaims, bool) {
	parsed, err := jwt.ParseWithClaims(tokenString, &TokenClaims{},
		func(t *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil {
		return nil, false
	}
	claims, ok := parsed.Claims.(*TokenClaims)
	if !ok || !parsed.Valid {
		return nil, false
	}
	if _, err := uuid.Parse(claims.UserID); err != nil {
		return nil, false
	}
	return claims, true
}

type RefreshClaims struct {
	UserID   string `json:"uid"`
	Username string `json:"usn"`
	Type     string `json:"typ"`
	jwt.RegisteredClaims
}

// MatchClaims is the short-lived match join token
type MatchClaims struct {
	UserID  string `json:"uid"`
	MatchID string `json:"mid"`
	Type    string `json:"typ"`
	jwt.RegisteredClaims
}

// GenerateMatchToken signs a match join token. Short TTL: tokens must be used promptly.
func GenerateMatchToken(secret string, userID uuid.UUID, matchID string, ttl time.Duration) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &MatchClaims{
		UserID:  userID.String(),
		MatchID: matchID,
		Type:    "match",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
	})
	return token.SignedString([]byte(secret))
}

// ParseMatchToken validates a match join token.
func ParseMatchToken(secret, tokenString string) (*MatchClaims, bool) {
	parsed, err := jwt.ParseWithClaims(tokenString, &MatchClaims{},
		func(t *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil {
		return nil, false
	}
	claims, ok := parsed.Claims.(*MatchClaims)
	if !ok || !parsed.Valid || claims.Type != "match" {
		return nil, false
	}
	return claims, true
}

// GenerateRefresh signs a refresh token
func GenerateRefresh(secret string, userID uuid.UUID, username string, ttl time.Duration) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, &RefreshClaims{
		UserID:   userID.String(),
		Username: username,
		Type:     "refresh",
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.NewString(),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
		},
	})
	return token.SignedString([]byte(secret))
}

// ParseRefresh validates a refresh token
func ParseRefresh(secret, tokenString string) (*RefreshClaims, bool) {
	parsed, err := jwt.ParseWithClaims(tokenString, &RefreshClaims{},
		func(t *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil {
		return nil, false
	}
	claims, ok := parsed.Claims.(*RefreshClaims)
	if !ok || !parsed.Valid || claims.Type != "refresh" {
		return nil, false
	}
	return claims, true
}
