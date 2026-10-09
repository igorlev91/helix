-- Tournaments
-- (core_tournament.go). A tournament is a leaderboard with a duration and
-- a limited number of score attempts per player.
-- sort_order: 0 = asc, 1 = desc. operator: 0 = best, 1 = set, 2 = incr.
CREATE TABLE IF NOT EXISTS tournaments (
    id           VARCHAR(128) PRIMARY KEY,
    sort_order   SMALLINT     NOT NULL DEFAULT 1,
    operator     SMALLINT     NOT NULL DEFAULT 0,
    duration_sec INT          NOT NULL DEFAULT 3600,  -- active window from creation
    max_attempts INT          NOT NULL DEFAULT 5,
    start_time   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    end_time     TIMESTAMPTZ  NOT NULL,               -- computed: start + duration
    metadata     JSONB        NOT NULL DEFAULT '{}',
    create_time  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tournament_records (
    tournament_id VARCHAR(128) NOT NULL REFERENCES tournaments(id) ON DELETE CASCADE,
    owner_id      UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username      VARCHAR(128) NOT NULL,
    score         BIGINT       NOT NULL DEFAULT 0,
    num_attempts  INT          NOT NULL DEFAULT 1,
    update_time   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (tournament_id, owner_id)
);
