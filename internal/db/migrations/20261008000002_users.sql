
CREATE TABLE IF NOT EXISTS users (
    id         UUID        PRIMARY KEY,
    username   TEXT        NOT NULL UNIQUE,
    device_id  TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Messages now belong to a user, not only to a session.
ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id);
