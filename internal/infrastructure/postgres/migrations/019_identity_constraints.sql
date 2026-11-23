-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Email addresses are stored normalised (domain.NormalizeEmail lower-cases
-- them), but nothing in the database stopped a mixed-case row. This index makes
-- the address unique regardless of case and lets lower(email) lookups use an
-- index. The case-sensitive users_email_key constraint stays: ON CONFLICT (email)
-- in users.sql needs it as its arbiter.
-- squawk-ignore require-concurrent-index-creation, prefer-robust-stmts -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the plain build blocks writes to users while it runs; the statement is kept exactly as specified, so a failed run is rolled back and nothing is left to rerun around
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email)) WHERE email IS NOT NULL;

-- Two uniqueness rules existed for users.mobile: the UNIQUE constraint
-- users_mobile_key (migration 002) and the partial unique index users_mobile_idx.
-- The constraint is kept, because ON CONFLICT (mobile) in users.sql names a
-- column without a predicate, and PostgreSQL cannot infer a partial index for
-- that; the partial index is dropped. NULLs never collide in either.
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the index is redundant with users_mobile_key and is dropped in the same transaction
DROP INDEX IF EXISTS users_mobile_idx;

-- The stored form of a mobile number is E.164: a plus, a non-zero leading digit
-- and 7 to 15 digits in all (domain.IsE164 uses the same pattern). The check is
-- added NOT VALID so the scan is deferred, then validated.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT, which takes a weaker lock; goose runs this file in one transaction
ALTER TABLE users ADD CONSTRAINT users_mobile_e164_chk CHECK (mobile ~ '^\+[1-9][0-9]{6,14}$') NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE users VALIDATE CONSTRAINT users_mobile_e164_chk;

-- An OTP challenge is sent by email or by SMS, and nothing else: the application
-- writes only domain.ChannelEmail and domain.ChannelSMS.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE otp_challenges ADD CONSTRAINT otp_challenges_channel_chk CHECK (channel IN ('email', 'sms')) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE otp_challenges VALIDATE CONSTRAINT otp_challenges_channel_chk;

-- DeleteExpiredChallenges removes challenges by expires_at. Without an index it
-- scans the whole table on every run.
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table holds challenges for minutes only
CREATE INDEX IF NOT EXISTS otp_challenges_expires_idx ON otp_challenges (expires_at);

-- A row that expires before it was created is always a bug in the writer. The
-- check is added NOT VALID, then validated, on each table that carries both
-- columns as NOT NULL: otp_challenges, refresh_tokens, organization_invites and
-- oauth_sign_in_states. api_tokens.expires_at is nullable (no expiry) and is
-- not covered.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE otp_challenges ADD CONSTRAINT otp_challenges_expiry_after_created_chk CHECK (expires_at > created_at) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE otp_challenges VALIDATE CONSTRAINT otp_challenges_expiry_after_created_chk;
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_expiry_after_created_chk CHECK (expires_at > created_at) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens VALIDATE CONSTRAINT refresh_tokens_expiry_after_created_chk;
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE organization_invites ADD CONSTRAINT organization_invites_expiry_after_created_chk CHECK (expires_at > created_at) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE organization_invites VALIDATE CONSTRAINT organization_invites_expiry_after_created_chk;
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE oauth_sign_in_states ADD CONSTRAINT oauth_sign_in_states_expiry_after_created_chk CHECK (expires_at > created_at) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE oauth_sign_in_states VALIDATE CONSTRAINT oauth_sign_in_states_expiry_after_created_chk;

-- +goose Down
-- squawk-ignore ban-drop-constraint -- rollback of the expiry checks added by this migration; no code depends on them after down
ALTER TABLE oauth_sign_in_states DROP CONSTRAINT IF EXISTS oauth_sign_in_states_expiry_after_created_chk;
-- squawk-ignore ban-drop-constraint -- rollback of the expiry checks added by this migration; no code depends on them after down
ALTER TABLE organization_invites DROP CONSTRAINT IF EXISTS organization_invites_expiry_after_created_chk;
-- squawk-ignore ban-drop-constraint -- rollback of the expiry checks added by this migration; no code depends on them after down
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_expiry_after_created_chk;
-- squawk-ignore ban-drop-constraint -- rollback of the expiry checks added by this migration; no code depends on them after down
ALTER TABLE otp_challenges DROP CONSTRAINT IF EXISTS otp_challenges_expiry_after_created_chk;
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; rollback of the index added by this migration
DROP INDEX IF EXISTS otp_challenges_expires_idx;
-- squawk-ignore ban-drop-constraint -- rollback of the channel check added by this migration; no client depends on it after down
ALTER TABLE otp_challenges DROP CONSTRAINT IF EXISTS otp_challenges_channel_chk;
-- squawk-ignore ban-drop-constraint -- rollback of the E.164 check added by this migration; no code depends on it after down
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_mobile_e164_chk;
-- Restores the partial index that 002 created, so the rollback leaves the same
-- uniqueness rules as before this migration.
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; rollback of the index dropped by this migration
CREATE UNIQUE INDEX IF NOT EXISTS users_mobile_idx ON users (mobile) WHERE mobile IS NOT NULL;
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; rollback of the index added by this migration
DROP INDEX IF EXISTS users_email_lower_key;
