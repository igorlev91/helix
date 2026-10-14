package server

import (
	"sync"
	"time"
)

// LoginAttemptLimiter
// counts failed password attempts per key (email) and locks the account out
// briefly after N failures to defeat brute-force.
type LoginAttemptLimiter struct {
	mu       sync.Mutex
	attempts map[string]*loginAttempts

	maxFailures int
	lockFor     time.Duration
}

type loginAttempts struct {
	failures int
	lockedTo time.Time
}

func NewLoginAttemptLimiter(maxFailures int, lockFor time.Duration) *LoginAttemptLimiter {
	return &LoginAttemptLimiter{
		attempts:    map[string]*loginAttempts{},
		maxFailures: maxFailures,
		lockFor:     lockFor,
	}
}

// IsLocked reports whether the key is currently locked out.
func (l *LoginAttemptLimiter) IsLocked(key string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[key]
	if !ok {
		return 0, false
	}
	remaining := time.Until(a.lockedTo)
	return remaining, remaining > 0
}

// RecordFailure increments the failure count; returns the lock duration
// once the threshold is crossed.
func (l *LoginAttemptLimiter) RecordFailure(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[key]
	if !ok {
		a = &loginAttempts{}
		l.attempts[key] = a
	}
	a.failures++
	if a.failures >= l.maxFailures {
		a.lockedTo = time.Now().Add(l.lockFor)
		return l.lockFor
	}
	return 0
}

// Reset clears the counter on successful login.
func (l *LoginAttemptLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
