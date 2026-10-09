package main

// Game-specific runtime hooks, analog of what a Nakama game ships as a Go
// plugin or Lua/JS module (server/runtime_go.go InitModule).
// In Nakama these live OUTSIDE the engine; here they are a separate file in
// main to mark the boundary: engine code never calls them directly, the
// router/runtime layer does.

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"helix/internal/notify"
	"helix/internal/runtime"
	"helix/internal/session"
)

func registerGameHooks(n *notify.Notifier) *runtime.Hooks {
	h := runtime.New()

	// Before-hook: chat moderation. Rejects messages containing banned words
	// (Nakama: RegisterBeforeChannelMessageSend can cancel the pipeline call).
	h.RegisterBefore("chat.send", func(s *session.Session, data json.RawMessage) (json.RawMessage, error) {
		var req struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &req); err != nil {
			return nil, nil
		}
		for _, bad := range []string{"spam", "cheat"} {
			if strings.Contains(strings.ToLower(req.Text), bad) {
				return nil, runtime.ErrHookRejected
			}
		}
		return nil, nil // nil = payload unchanged
	})

	// After-hook: observe chat activity (Nakama: RegisterAfter* side effects).
	h.RegisterAfter("chat.send", func(s *session.Session, op string, data json.RawMessage, result any, err error) {
		if err == nil {
			log.Printf("hook: %s sent a chat message", s.Username)
		}
	})

	// RPC: custom game op that doesn't exist in the engine.
	// (Nakama: RegisterRpc("get_daily_reward", ...) in game code.)
	h.RegisterRpc("daily_reward", func(s *session.Session, data json.RawMessage) (any, error) {
		return map[string]any{
			"coins":    100,
			"username": s.Username,
			"note":     "game-code RPC, no engine op needed",
		}, nil
	})

	// RPC: server-driven notification demo (game code pushes to self).
	h.RegisterRpc("ping_me", func(s *session.Session, data json.RawMessage) (any, error) {
		if err := n.Send(context.Background(), s.UserID, "pong from RPC", 99, map[string]bool{}, false, &s.UserID); err != nil {
			return nil, err
		}
		return map[string]bool{"queued": true}, nil
	})

	return h
}
