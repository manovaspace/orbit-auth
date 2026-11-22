-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Account states. A disabled account cannot sign in, refresh, select an
-- organization, verify an OTP, use an API token or act on a membership; staff
-- disable and enable it (ADR-061). The check pins the three values; the
-- transition rules are code. Every existing row holds 'active' or
-- 'signup_pending', so the constraint is added NOT VALID and then validated.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT, which takes a weaker lock; the table holds one row per account and goose runs this file in one transaction
ALTER TABLE users
    ADD CONSTRAINT users_status_chk CHECK (status IN ('active', 'disabled', 'signup_pending')) NOT VALID;
ALTER TABLE users VALIDATE CONSTRAINT users_status_chk;
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;
-- squawk-ignore adding-not-nullable-field, prefer-robust-stmts -- a constant default ('') keeps the column cheap to add; the table holds one row per account
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_reason TEXT NOT NULL DEFAULT '';

-- Disabling an account and removing a member revoke that user's live refresh
-- tokens. The partial index covers exactly those rows.
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the partial index covers live tokens only and the table is written by sign-in, not bulk-scanned
CREATE INDEX IF NOT EXISTS refresh_tokens_user_live_idx
    ON refresh_tokens (user_id)
    WHERE revoked_at IS NULL;

-- Durable audit trail of account, membership, organization and invitation
-- changes. Each row is written in the same transaction as the change it
-- records. Rows deliberately carry no foreign keys, so they outlive the
-- organization and the user. A row never holds an email, a mobile number, a
-- password, a token or a hash: an invitation is identified by invite_id.
CREATE TABLE IF NOT EXISTS audit_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    action TEXT NOT NULL CHECK (action <> ''),
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('user', 'staff', 'system')),
    actor_id TEXT NOT NULL DEFAULT '',
    org_id UUID,
    subject_user_id UUID,
    invite_id UUID,
    reason TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'::jsonb
);
-- squawk-ignore require-concurrent-index-creation -- the table was just created in this transaction
CREATE INDEX IF NOT EXISTS audit_events_org_idx ON audit_events (org_id, id DESC);
-- squawk-ignore require-concurrent-index-creation -- the table was just created in this transaction
CREATE INDEX IF NOT EXISTS audit_events_subject_idx ON audit_events (subject_user_id, id DESC);

-- +goose Down
-- squawk-ignore ban-drop-table -- rollback of the audit table added by this migration; no code reads it after down
DROP TABLE IF EXISTS audit_events;
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the index is dropped with the rollback
DROP INDEX IF EXISTS refresh_tokens_user_live_idx;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE users DROP COLUMN IF EXISTS disabled_reason;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE users DROP COLUMN IF EXISTS disabled_at;
-- squawk-ignore ban-drop-constraint -- rollback of the status check added by this migration; no client depends on it after down
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_chk;
