package server

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"

	"helix/internal/session"
)


func NewWsHandler() http.HandlerFunc {
	upgrader := &websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}

	return func(w http.ResponseWriter, r *http.Request) {
	

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("ws upgrade failed: %v", err)
			return
		}

		log.Printf("ws client connected from %s", r.RemoteAddr)

		s := session.New(conn,
		
			func(s *session.Session, data []byte) {
				s.Send(data)
			},
			// onClose — позже: снятие сессии из реестра.
			func(s *session.Session) {},
		)

	
		s.Consume()
	}
}
