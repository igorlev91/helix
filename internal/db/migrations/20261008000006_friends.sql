-- Friend graph, analog of Nakama's user_edge table.
-- state: 0 = friend, 1 = invite_sent, 2 = invite_received, 3 = blocked.
-- A relationship is stored as TWO directed rows (A->B and B->A).
CREATE TABLE IF NOT EXISTS friends (
    source_id      UUID      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    destination_id UUID      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state          SMALLINT  NOT NULL DEFAULT 0,
    update_time    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, destination_id)
);
