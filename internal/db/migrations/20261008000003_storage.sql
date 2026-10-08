-- Generic JSON storage
-- (migrate/sql/20180103142001_initial_schema.sql).
-- read/write permissions: 0 = none, 1 = owner only, 2 = public.
CREATE TABLE IF NOT EXISTS storage (
    collection  VARCHAR(128) NOT NULL,
    key         VARCHAR(128) NOT NULL,
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    value       JSONB        NOT NULL DEFAULT '{}',
    version     VARCHAR(32)  NOT NULL,  -- md5 of value, enables optimistic concurrency
    read        SMALLINT     NOT NULL DEFAULT 1 CHECK (read >= 0),
    write       SMALLINT     NOT NULL DEFAULT 1 CHECK (write >= 0),
    create_time TIMESTAMPTZ  NOT NULL DEFAULT now(),
    update_time TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (collection, key, user_id)
);

CREATE INDEX IF NOT EXISTS idx_storage_collection_user ON storage (collection, user_id);
