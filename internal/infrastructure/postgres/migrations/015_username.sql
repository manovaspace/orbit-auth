-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- A username is an optional login identifier. NULL means the account has none.
-- Usernames are always stored lower case. The check pins the format; the
-- normalisation rules and the reserved-shape rules are code
-- (domain.NormalizeUsername). The index allows many NULLs.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS username TEXT
        CONSTRAINT users_username_format
        CHECK (username IS NULL OR username ~ '^[a-z][a-z0-9._-]{2,31}$');
CREATE UNIQUE INDEX IF NOT EXISTS users_username_idx ON users (username);

-- +goose Down
DROP INDEX IF EXISTS users_username_idx;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE users DROP COLUMN IF EXISTS username;
