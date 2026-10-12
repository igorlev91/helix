package status

import (
	"database/sql"
	"sync"

	"github.com/google/uuid"
)

// Registry tracks per-user status messages and follow relations.
//   - a user sets a status message ("racing", "afk", ...)
//   - friends FOLLOW each other's status (follow = see their presence)
//   - followers get pushes when the followed user updates status
//     or comes online / goes offline
type Registry struct {
	mu       sync.RWMutex
	statuses map[uuid.UUID]string             // user -> status message
	follows  map[uuid.UUID]map[uuid.UUID]bool // follower -> set of followed users
	online   map[uuid.UUID]int                // user -> online session count
}

func NewRegistry() *Registry {
	return &Registry{
		statuses: map[uuid.UUID]string{},
		follows:  map[uuid.UUID]map[uuid.UUID]bool{},
		online:   map[uuid.UUID]int{},
	}
}

// Follow makes follower observe target's presence
func (r *Registry) Follow(follower, target uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.follows[follower] == nil {
		r.follows[follower] = map[uuid.UUID]bool{}
	}
	r.follows[follower][target] = true
}

// Unfollow drops one follow relation (friend removal).
func (r *Registry) Unfollow(follower, target uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if set := r.follows[follower]; set != nil {
		delete(set, target)
	}
}

// FollowersOf returns users who follow the given user.
func (r *Registry) FollowersOf(userID uuid.UUID) []uuid.UUID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []uuid.UUID
	for follower, set := range r.follows {
		if set[userID] {
			out = append(out, follower)
		}
	}
	return out
}

// FollowingOf returns users the given user follows.
func (r *Registry) FollowingOf(userID uuid.UUID) []uuid.UUID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []uuid.UUID
	for target := range r.follows[userID] {
		out = append(out, target)
	}
	return out
}

// Set updates a user's status message
func (r *Registry) Set(userID uuid.UUID, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[userID] = message
}

// Get reads a user's status message.
func (r *Registry) Get(userID uuid.UUID) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statuses[userID]
}

// Online increments the session counter; returns true if this is the
// FIRST session (user just came online).
func (r *Registry) Online(userID uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.online[userID]++
	return r.online[userID] == 1
}

// Offline decrements; returns true when the LAST session closed
// (user went fully offline).
func (r *Registry) Offline(userID uuid.UUID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.online[userID] > 0 {
		r.online[userID]--
	}
	return r.online[userID] == 0
}

// IsOnline reports whether a user has any live session.
func (r *Registry) IsOnline(userID uuid.UUID) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.online[userID] > 0
}

// LoadFriendsFollows rebuilds follow relations from the friends table
func (r *Registry) LoadFriendsFollows(db *sql.DB) error {
	rows, err := db.Query(`SELECT source_id, destination_id FROM friends WHERE state = 0`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var src, dst uuid.UUID
		if err := rows.Scan(&src, &dst); err != nil {
			return err
		}
		r.Follow(src, dst)
	}
	return rows.Err()
}
