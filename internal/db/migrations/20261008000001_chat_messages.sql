-- Chat message history, simplified analog of Nakama's channel message storage
-- (message history lives in the "message" table there; see core_channel.go).
CREATE TABLE IF NOT EXISTS chat_messages (
    id         BIGSERIAL PRIMARY KEY,
    channel    TEXT        NOT NULL,
    session_id UUID        NOT NULL,
    text       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_chat_messages_channel_id
    ON chat_messages (channel, id DESC);
