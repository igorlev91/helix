-- Notifications
-- (core_notification.go). Persistent rows can be listed later;
-- live-only notifications are pushed and never stored.
CREATE TABLE IF NOT EXISTS notifications (
    id          BIGSERIAL    PRIMARY KEY,
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    subject     TEXT         NOT NULL,
    content     JSONB        NOT NULL DEFAULT '{}',
    code        INT          NOT NULL DEFAULT 0,  -- app-specific meaning (Nakama: same)
    sender_id   UUID,
    create_time TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications (user_id, id DESC);
