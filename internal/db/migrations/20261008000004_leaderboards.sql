-- Leaderboards,
-- (migrate/sql/20180103142001_initial_schema.sql).
-- sort_order: 0 = asc, 1 = desc. operator: 0 = best, 1 = set, 2 = incr, 3 = decr.
-- Nakama additionally has reset_schedule (cron) + expiry_time for seasonal boards.
CREATE TABLE IF NOT EXISTS leaderboards (
    id          VARCHAR(128) PRIMARY KEY,
    sort_order  SMALLINT     NOT NULL DEFAULT 1,
    operator    SMALLINT     NOT NULL DEFAULT 0,
    metadata    JSONB        NOT NULL DEFAULT '{}',
    create_time TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- One record per owner per board (UNIQUE (owner_id, leaderboard_id, expiry_time)).
CREATE TABLE IF NOT EXISTS leaderboard_records (
    leaderboard_id VARCHAR(128) NOT NULL REFERENCES leaderboards(id) ON DELETE CASCADE,
    owner_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username       VARCHAR(128) NOT NULL,
    score          BIGINT       NOT NULL DEFAULT 0,
    num_score      INT          NOT NULL DEFAULT 1,
    metadata       JSONB        NOT NULL DEFAULT '{}',
    update_time    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (leaderboard_id, owner_id)
);
