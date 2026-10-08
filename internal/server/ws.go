package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"

	"helix/internal/session"
)

// NewWsHandler returns an HTTP handler that upgrades connections to WebSocket.
// validate params -> upgrade -> create session -> register -> Consume.
func NewWsHandler(registry *session.Registry) http.HandlerFunc {
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	return func(w http.ResponseWriter, r *http.Request) {
		// TODO(auth step): validate token 

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed: %v", err)
			return
		}

		log.Printf("ws client connected from %s", r.RemoteAddr)

		s := session.New(conn,
			// onMessage: echo for now; the pipeline lands here later.
			func(s *session.Session, data []byte) {
				s.Send(data)
			},
			// onClose: unregister the session.
			func(s *session.Session) {
				registry.Remove(s.ID)
				log.Printf("ws session removed, online=%d", registry.Count())
			},
		)

		// Register the online session 
		registry.Add(s)
		log.Printf("ws session registered %s, online=%d", s.ID, registry.Count())

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
