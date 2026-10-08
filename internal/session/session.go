package session

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Session is a live WebSocket client session.

// Same two-loop model:
//
//	Consume()         - inbound: reads from the socket and dispatches messages
//	processOutgoing() - outbound: writes queued payloads and sends pings
type Session struct {
	ID   uuid.UUID
	conn *websocket.Conn

	ctx       context.Context
	ctxCancel context.CancelFunc

	outgoingCh chan []byte // outbound queue (Nakama: OutgoingQueueSize)
	pingPeriod time.Duration
	pongWait   time.Duration
	writeWait  time.Duration

	closeOnce sync.Once
	onMessage func(s *Session, data []byte) // the pipeline will land here later
	onClose   func(s *Session)              // registry removal hook
}

func New(conn *websocket.Conn, onMessage func(*Session, []byte), onClose func(*Session)) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	return &Session{
		ID:         uuid.New(),
		conn:       conn,
		ctx:        ctx,
		ctxCancel:  cancel,
		outgoingCh: make(chan []byte, 64),
		pingPeriod: 25 * time.Second,
		pongWait:   30 * time.Second,
		writeWait:  10 * time.Second,
		onMessage:  onMessage,
		onClose:    onClose,
	}
}

// Send queues an outbound payload without blocking.
func (s *Session) Send(data []byte) {
	select {
	case s.outgoingCh <- data:
	default:
		log.Printf("session %s: outgoing queue full, dropping", s.ID)
	}
}

// Consume runs the inbound loop and blocks until the connection closes

func (s *Session) Consume() {
	go s.processOutgoing()

	s.conn.SetReadLimit(64 * 1024)
	_ = s.conn.SetReadDeadline(time.Now().Add(s.pongWait))
	s.conn.SetPongHandler(func(string) error {
		// Client answered our ping: extend the read deadline

		return s.conn.SetReadDeadline(time.Now().Add(s.pongWait))
	})

	var reason string
	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			// "Normal" closures are not logged 
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				reason = err.Error()
			}
			break
		}
		s.onMessage(s, data)
	}

	s.Close(reason)
}

// processOutgoing runs the outbound loop 
func (s *Session) processOutgoing() {
	ping := time.NewTicker(s.pingPeriod)
	defer ping.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ping.C:
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeWait))
			if err := s.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case payload := <-s.outgoingCh:
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeWait))
			if err := s.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}
		}
	}
}

// Close shuts the session down exactly once 
func (s *Session) Close(reason string) {
	s.closeOnce.Do(func() {
		if reason != "" {
			log.Printf("session %s closed: %s", s.ID, reason)
		} else {
			log.Printf("session %s closed", s.ID)
		}
		s.ctxCancel()
		_ = s.conn.Close()
		if s.onClose != nil {
			s.onClose(s)
		}
	})
}
