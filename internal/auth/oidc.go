package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OIDC verification for external identity providers (Apple, Google),
// the client sends the provider's ID token; we verify it against the provider's JWKS and
// trust the "sub" claim as the external identity.

// JWKSClient fetches and caches a provider's JSON Web Key Set.
type JWKSClient struct {
	url     string
	ttl     time.Duration
	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey // by kid
	fetched time.Time
	http    *http.Client
}

func NewJWKSClient(url string) *JWKSClient {
	return &JWKSClient{
		url:  url,
		ttl:  time.Hour,
		keys: map[string]*rsa.PublicKey{},
		http: &http.Client{Timeout: 5 * time.Second},
	}
}

type jwksDoc struct {
	Keys []struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Alg string `json:"alg"`
		Use string `json:"use"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// refresh downloads the JWKS document.
func (c *JWKSClient) refresh() error {
	resp, err := c.http.Get(c.url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks fetch: %s", resp.Status)
	}
	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		var e int
		for _, b := range eb {
			e = e<<8 | int(b)
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}
	}
	c.mu.Lock()
	c.keys = keys
	c.fetched = time.Now()
	c.mu.Unlock()
	return nil
}

// key returns the RSA key for a kid, refreshing the cache when stale/missing.
func (c *JWKSClient) key(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	k, ok := c.keys[kid]
	stale := time.Since(c.fetched) > c.ttl
	c.mu.RUnlock()
	if !ok || stale {
		if err := c.refresh(); err != nil {
			return nil, err
		}
		c.mu.RLock()
		k, ok = c.keys[kid]
		c.mu.RUnlock()
	}
	if !ok {
		return nil, errors.New("unknown signing key")
	}
	return k, nil
}

// OIDCClaims are the claims we care about from a provider ID token.
type OIDCClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	jwt.RegisteredClaims
}

// VerifyOIDCToken validates a provider ID token: RS256 signature via the
// provider JWKS, required expiry, issuer and audience. Returns the
// verified claims (sub = stable external user id).
func VerifyOIDCToken(jwks *JWKSClient, issuer, audience, tokenString string) (*OIDCClaims, error) {
	claims, err := jwt.ParseWithClaims(tokenString, &OIDCClaims{},
		func(t *jwt.Token) (any, error) {
			kid, _ := t.Header["kid"].(string)
			if kid == "" {
				return nil, errors.New("missing kid")
			}
			return jwks.key(kid)
		},
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
	)
	if err != nil {
		return nil, err
	}
	c, ok := claims.Claims.(*OIDCClaims)
	if !ok || c.Subject == "" {
		return nil, errors.New("invalid claims")
	}
	return c, nil
}
