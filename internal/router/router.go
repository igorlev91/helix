package router

import (
	"encoding/json"
	"log"

	"helix/internal/runtime"
	"helix/internal/session"
)

// Envelope is the wire format of every client message.
// Cid correlates a request with its response (the client matches it against a pending promise), Op selects
// the handler
type Envelope struct {
	Cid  string          `json:"cid,omitempty"`
	Op   string          `json:"op"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Handler processes one op and returns the response payload.

type Handler func(s *session.Session, data json.RawMessage) (any, error)

// Router dispatches inbound envelopes to handlers by op.
type Router struct {
	handlers map[string]Handler
	hooks    *runtime.Hooks
}

func New(hooks *runtime.Hooks) *Router {
	if hooks == nil {
		hooks = runtime.New()
	}
	return &Router{handlers: make(map[string]Handler), hooks: hooks}
}

func (r *Router) Register(op string, h Handler) {
	r.handlers[op] = h
}

// SendToStream pushes an envelope to every session in a stream.
func SendToStream(sessions []*session.Session, op string, data any) {
	if len(sessions) == 0 {
		return
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	payload, err := json.Marshal(Envelope{Op: op, Data: raw})
	if err != nil {
		return
	}
	for _, s := range sessions {
		s.Send(payload)
	}
}

// Route parses one inbound payload, dispatches it and replies with the
// same Cid so the client can match the response to its request.
//
// Order (mirrors Nakama's runtime pipeline):
//  1. runtime RPC op? -> game code handles it entirely
//  2. before-hooks (may reject or rewrite the payload)
//  3. built-in handler
//  4. after-hooks (observe, never change the response)
func (r *Router) Route(s *session.Session, payload []byte) {
	var in Envelope
	if err := json.Unmarshal(payload, &in); err != nil {
		reply(s, "", in.Op, map[string]string{"error": "malformed payload"})
		return
	}

	// 1. Game-code RPC ops shadow everything (Nakama: Envelope_Rpc).
	if fn, ok := r.hooks.Rpc(in.Op); ok {
		out, err := fn(s, in.Data)
		if err != nil {
			reply(s, in.Cid, in.Op, map[string]string{"error": err.Error()})
			return
		}
		reply(s, in.Cid, in.Op, out)
		return
	}

	h, ok := r.handlers[in.Op]
	if !ok {
		reply(s, in.Cid, in.Op, map[string]string{"error": "unknown op: " + in.Op})
		return
	}

	// 2. Before-hooks (may reject or rewrite the payload).
	data, err := r.hooks.RunBefore(s, in.Op, in.Data)
	if err != nil {
		reply(s, in.Cid, in.Op, map[string]string{"error": err.Error()})
		return
	}

	// 3. Built-in handler.
	out, err := h(s, data)

	// 4. After-hooks (side effects only, never change the response).
	r.hooks.RunAfter(s, in.Op, data, out, err)

	if err != nil {
		log.Printf("router: op=%s failed: %v", in.Op, err)
		reply(s, in.Cid, in.Op, map[string]string{"error": err.Error()})
		return
	}
	reply(s, in.Cid, in.Op, out)
}

func reply(s *session.Session, cid, op string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	out, err := json.Marshal(Envelope{Cid: cid, Op: op, Data: raw})
	if err != nil {
		return
	}
	s.Send(out)
}
