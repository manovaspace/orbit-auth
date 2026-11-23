-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- An OTP challenge now carries the purpose it was issued for: a sign-in code,
-- a phone invitation code or an email sign-up code. Before this migration the
-- challenges of the three flows shared one key, so a code of one flow answered
-- the others.
--
-- The default stays on purpose. The binary that is still running during a
-- rollout inserts challenges without a purpose, and every challenge that exists
-- when this runs (they live minutes) is treated as a sign-in code. New code
-- always writes the purpose explicitly; the store refuses an empty one.
-- squawk-ignore prefer-robust-stmts -- ADD COLUMN with a constant default is a catalog-only change on PostgreSQL 11 and later; goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE otp_challenges ADD COLUMN purpose text NOT NULL DEFAULT 'sign_in';

-- A challenge purpose is one of the three flows, and nothing else. The check is
-- added NOT VALID so the scan is deferred, then validated.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE otp_challenges ADD CONSTRAINT otp_challenges_purpose_chk CHECK (purpose IN ('sign_in', 'invite', 'signup')) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE otp_challenges VALIDATE CONSTRAINT otp_challenges_purpose_chk;

-- GetLatestChallenge and DeleteChallengesForIdentifier look up one flow's
-- challenges for an identifier and channel, newest first. The old index on
-- (identifier, channel, created_at) does not carry the purpose, so it is
-- replaced.
-- squawk-ignore require-concurrent-index-creation, prefer-robust-stmts -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table holds challenges for minutes only; the index is kept exactly as specified (no IF NOT EXISTS), so a failed run is rolled back and nothing is left to rerun around
CREATE INDEX otp_challenges_lookup_idx ON otp_challenges (identifier, channel, purpose, created_at DESC);
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table holds challenges for minutes only, and the index is replaced by otp_challenges_lookup_idx in the same transaction
DROP INDEX IF EXISTS otp_challenges_identifier_idx;

-- +goose Down
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; rollback of the index dropped by this migration; the table holds challenges for minutes only
CREATE INDEX IF NOT EXISTS otp_challenges_identifier_idx ON otp_challenges (identifier, channel, created_at DESC);
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; rollback of the index added by this migration
DROP INDEX IF EXISTS otp_challenges_lookup_idx;
-- squawk-ignore ban-drop-constraint -- rollback of the purpose check added by this migration; no code depends on it after down
ALTER TABLE otp_challenges DROP CONSTRAINT IF EXISTS otp_challenges_purpose_chk;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; the purpose of a challenge is not used after down
ALTER TABLE otp_challenges DROP COLUMN IF EXISTS purpose;
