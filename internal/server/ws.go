package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"helix/internal/auth"
	"helix/internal/match"
	"helix/internal/matchmaker"
	"helix/internal/router"
	"helix/internal/session"
	"helix/internal/tracker"
)

// NewWsHandler returns an HTTP handler that upgrades connections to WebSocket.
// validate token -> upgrade -> create session -> register -> Consume.
func NewWsHandler(registry *session.Registry, rt *router.Router, tr *tracker.Tracker, mm *matchmaker.Matchmaker, matches *match.Registry, secret string) http.HandlerFunc {
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// Auth: Bearer header or ?token=
		token := r.URL.Query().Get("token")
		if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
			token = strings.TrimPrefix(header, "Bearer ")
		}
		claims, ok := auth.Parse(secret, token)
		if !ok {
			http.Error(w, "Missing or invalid token", http.StatusUnauthorized)
			return
		}
		userID, err := uuid.Parse(claims.UserID)
		if err != nil {
			http.Error(w, "Missing or invalid token", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed: %v", err)
			return
		}

		log.Printf("ws client connected user=%s from %s", claims.Username, r.RemoteAddr)

		s := session.New(conn, userID, claims.Username,
			// onMessage: dispatch through the router
			func(s *session.Session, data []byte) {
				rt.Route(s, data)
			},
			// onClose: unregister, untrack, cancel matchmaking, leave the match.
			func(s *session.Session) {
				registry.Remove(s.ID)
				tr.LeaveAll(s)
				mm.Remove(s.ID)
				if s.MatchID != "" {
					if m, ok := matches.Get(s.MatchID); ok {
						m.RemovePresence(s.ID)
					}
				}
				log.Printf("ws session removed, online=%d", registry.Count())
			},
		)

		// Register the online session
		registry.Add(s)
		log.Printf("ws session registered %s user=%s, online=%d", s.ID, s.Username, registry.Count())

		// Block until the session closes (Nakama: session.Consume()).
		s.Consume()
	}
}

// NewSessionsHandler is a debug endpoint listing online session IDs.
func NewSessionsHandler(registry *session.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"count": registry.Count(),
			"ids":   registry.IDs(),
		})
	}
}
