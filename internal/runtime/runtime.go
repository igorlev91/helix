package runtime

import (
	"encoding/json"
	"errors"

	"helix/internal/session"
)

// Hooks is the game-code extension point, helix hooks are plain Go functions registered at startup —
// same interception model, no plugin machinery.
//
// Three extension kinds
//   - Before hook: runs BEFORE an op, may reject or rewrite the payload
//   - After hook:  runs AFTER an op (observe/metrics/side effects)
//   - RPC:         a brand new op registered by game code
type Hooks struct {
	before map[string][]BeforeFn
	after  map[string][]AfterFn
	rpc    map[string]RpcFn
}

// BeforeFn may reject the request (return error) or rewrite the payload.
type BeforeFn func(s *session.Session, data json.RawMessage) (json.RawMessage, error)

// AfterFn observes the result; errors are logged but don't affect the client.
type AfterFn func(s *session.Session, op string, data json.RawMessage, result any, err error)

// RpcFn is a game-code op (Nakama: RuntimeRpcFunction).
type RpcFn func(s *session.Session, data json.RawMessage) (any, error)

func New() *Hooks {
	return &Hooks{
		before: map[string][]BeforeFn{},
		after:  map[string][]AfterFn{},
		rpc:    map[string]RpcFn{},
	}
}

// RegisterBefore attaches a before-hook to a built-in op
func (h *Hooks) RegisterBefore(op string, fn BeforeFn) {
	h.before[op] = append(h.before[op], fn)
}

// RegisterAfter attaches an after-hook to a built-in op
func (h *Hooks) RegisterAfter(op string, fn AfterFn) {
	h.after[op] = append(h.after[op], fn)
}

// RegisterRpc adds a new game-code op
func (h *Hooks) RegisterRpc(op string, fn RpcFn) {
	h.rpc[op] = fn
}

// RunBefore executes the before chain. Returns the (possibly rewritten)
// payload or the first hook's error.
func (h *Hooks) RunBefore(s *session.Session, op string, data json.RawMessage) (json.RawMessage, error) {
	for _, fn := range h.before[op] {
		out, err := fn(s, data)
		if err != nil {
			return nil, err
		}
		if out != nil {
			data = out
		}
	}
	return data, nil
}

// RunAfter executes after-hooks; failures are logged only.
func (h *Hooks) RunAfter(s *session.Session, op string, data json.RawMessage, result any, err error) {
	for _, fn := range h.after[op] {
		fn(s, op, data, result, err)
	}
}

// Rpc returns a registered game-code op (Nakama: api RPC path).
func (h *Hooks) Rpc(op string) (RpcFn, bool) {
	fn, ok := h.rpc[op]
	return fn, ok
}

// ErrHookRejected is the canonical "hook blocked the request" error
var ErrHookRejected = errors.New("rejected by runtime hook")
