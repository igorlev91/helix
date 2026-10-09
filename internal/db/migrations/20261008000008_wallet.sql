-- Virtual currency wallet
-- (core_wallet.go). Append-only ledger: the balance is the SUM of all
-- changesets. Nothing is ever UPDATEd or DELETEd — full audit trail.
CREATE TABLE IF NOT EXISTS wallet_ledger (
    id          UUID        NOT NULL UNIQUE,
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    changeset   JSONB       NOT NULL,  -- {"coins": +100} / {"coins": -30}
    metadata    JSONB       NOT NULL DEFAULT '{}',
    create_time TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, create_time, id)
);
