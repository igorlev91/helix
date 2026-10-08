package router

import (
	"encoding/json"
	"errors"

	"helix/internal/session"
	"helix/internal/tracker"
)


// A "channel" maps to a stream named "chat:<channel>".
type chatJoinRequest struct {
	Channel string `json:"channel"`
}

type chatSendRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

// RegisterChatOps installs chat.join / chat.leave / chat.send.
func (r *Router) RegisterChatOps(tr *tracker.Tracker) {
	streamName := func(channel string) string { return "chat:" + channel }

	r.Register("chat.join", func(s *session.Session, data json.RawMessage) (any, error) {
		var req chatJoinRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		tr.Join(streamName(req.Channel), s)

		// Notify the stream about the new presence
		SendToStream(tr.List(streamName(req.Channel)), "chat.presence", map[string]any{
			"channel":    req.Channel,
			"session_id": s.ID.String(),
			"event":      "join",
		})
		return map[string]string{"joined": req.Channel}, nil
	})

	r.Register("chat.leave", func(s *session.Session, data json.RawMessage) (any, error) {
		var req chatJoinRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		tr.Leave(streamName(req.Channel), s)
		SendToStream(tr.List(streamName(req.Channel)), "chat.presence", map[string]any{
			"channel":    req.Channel,
			"session_id": s.ID.String(),
			"event":      "leave",
		})
		return map[string]string{"left": req.Channel}, nil
	})

	r.Register("chat.send", func(s *session.Session, data json.RawMessage) (any, error) {
		var req chatSendRequest
		if err := json.Unmarshal(data, &req); err != nil || req.Channel == "" {
			return nil, errors.New("channel required")
		}
		stream := streamName(req.Channel)
		// Sender must be in the channel to post to it.
		for _, member := range tr.List(stream) {
			if member.ID == s.ID {
				SendToStream(tr.List(stream), "chat.message", map[string]any{
					"channel":    req.Channel,
					"session_id": s.ID.String(),
					"text":       req.Text,
				})
				return map[string]string{"sent": req.Channel}, nil
			}
		}
		return nil, errors.New("not in channel")
	})
}
