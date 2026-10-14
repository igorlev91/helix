package auth

import (
	"sync"
	"time"
)

// SessionCache  a validated JWT is cached by its exact string until its expiry, so repeat requests with
// the same token skip signature verification entirely (the HMAC verify
// is cheap, but at connection rates it adds up — and the cache also
// gives a single eviction point for expired sessions).
type SessionCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	maxSize int
}

type cacheEntry struct {
	claims  *TokenClaims
	expires time.Time
}

func NewSessionCache() *SessionCache {
	return &SessionCache{entries: map[string]*cacheEntry{}, maxSize: 10000}
}

// Parse returns cached claims when fresh, otherwise validates the token
// and caches the result.
func (c *SessionCache) Parse(secret, tokenString string) (*TokenClaims, bool) {
	now := time.Now()
	c.mu.RLock()
	e, ok := c.entries[tokenString]
	c.mu.RUnlock()
	if ok && now.Before(e.expires) {
		return e.claims, true
	}

	claims, valid := Parse(secret, tokenString)
	if !valid || claims.ExpiresAt == nil {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.maxSize {
		c.evictExpiredLocked(now)
		if len(c.entries) >= c.maxSize {
			c.dropHalfLocked() // last resort: cache pressure, drop half
		}
	}
	c.entries[tokenString] = &cacheEntry{claims: claims, expires: claims.ExpiresAt.Time}
	return claims, true
}

// Evict removes one token (e.g. on forced logout).
func (c *SessionCache) Evict(tokenString string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, tokenString)
}

// Size reports cached entries (debug endpoint material).
func (c *SessionCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *SessionCache) evictExpiredLocked(now time.Time) {
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
}

func (c *SessionCache) dropHalfLocked() {
	n := 0
	for k := range c.entries {
		delete(c.entries, k)
		n++
		if n >= c.maxSize/2 {
			return
		}
	}
}
