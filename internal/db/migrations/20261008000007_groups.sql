-- Groups (clans)
-- (core_group.go). state on the group: 0 = open (join instantly),
-- 1 = closed (join requires approval). state on membership:
-- 0 = superadmin, 1 = admin, 2 = member, 3 = join_request.
CREATE TABLE IF NOT EXISTS groups (
    id           UUID         PRIMARY KEY,
    creator_id   UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         VARCHAR(255) NOT NULL UNIQUE,
    description  VARCHAR(255) NOT NULL DEFAULT '',
    state        SMALLINT     NOT NULL DEFAULT 0 CHECK (state >= 0),
    edge_count   INT          NOT NULL DEFAULT 1 CHECK (edge_count >= 1),
    max_count    INT          NOT NULL DEFAULT 100 CHECK (max_count >= 1),
    create_time  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS group_users (
    group_id    UUID      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id     UUID      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    state       SMALLINT  NOT NULL DEFAULT 2,
    update_time TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);
