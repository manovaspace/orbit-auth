-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- A Google sign-in in flight. The row holds sha256(state), never the state. It
-- is single use: ConsumeOAuthSignInState deletes it, and an expired row is
-- never consumed. The check pins the hash format.
CREATE TABLE IF NOT EXISTS oauth_sign_in_states (
    state_hash TEXT PRIMARY KEY CHECK (state_hash ~ '^[0-9a-f]{64}$'),
    redirect_uri TEXT NOT NULL,
    return_to TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- squawk-ignore require-concurrent-index-creation -- the table was just created in this transaction
CREATE INDEX IF NOT EXISTS oauth_sign_in_states_expires_idx ON oauth_sign_in_states (expires_at);

-- An external identity linked to a product user. A subject is never relinked to
-- another user, and a user has at most one identity per provider.
CREATE TABLE IF NOT EXISTS user_identities (
    provider TEXT NOT NULL CHECK (provider IN ('google')),
    subject TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, subject),
    UNIQUE (provider, user_id)
);

-- +goose Down
-- squawk-ignore ban-drop-table -- rollback of the identity table added by this migration; no code reads it after down
DROP TABLE IF EXISTS user_identities;
-- squawk-ignore ban-drop-table -- rollback of the state table added by this migration; no code reads it after down
DROP TABLE IF EXISTS oauth_sign_in_states;
