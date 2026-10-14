-- Multi-provider accounts: device_id is only one of several identities.
ALTER TABLE users ALTER COLUMN device_id DROP NOT NULL;
