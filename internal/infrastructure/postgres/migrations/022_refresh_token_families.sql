-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- A refresh token belongs to a family: every token issued from one sign-in, and
-- every token rotated from it, shares a family_id. Presenting a token that was
-- already rotated means it was copied, so the whole family is revoked (OAuth
-- 2.0 Security BCP, refresh token rotation). rotated_at is set when a token is
-- consumed by a rotation, and stays NULL for a logout or revoke.
-- squawk-ignore prefer-robust-stmts -- ADD COLUMN without a default is a catalog-only change; goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens ADD COLUMN family_id uuid;
-- squawk-ignore prefer-robust-stmts -- ADD COLUMN without a default is a catalog-only change; goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens ADD COLUMN rotated_at timestamptz;

-- Every token issued before this migration is its own family, so an old token
-- that is reused later is still traced to a family of one.
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind; the backfill is bounded by the refresh token table, which holds one row per live sign-in
UPDATE refresh_tokens SET family_id = id WHERE family_id IS NULL;

-- SET NOT NULL needs a full scan under ACCESS EXCLUSIVE. A validated CHECK
-- constraint proves the same fact first, so PostgreSQL skips the scan; the
-- temporary check is dropped once the column is NOT NULL.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_family_not_null_chk CHECK (family_id IS NOT NULL) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens VALIDATE CONSTRAINT refresh_tokens_family_not_null_chk;
-- squawk-ignore prefer-robust-stmts -- the validated check above proves no row has a NULL family_id, so SET NOT NULL takes no scan; goose runs this file in one transaction
ALTER TABLE refresh_tokens ALTER COLUMN family_id SET NOT NULL;
-- squawk-ignore ban-drop-constraint, prefer-robust-stmts -- drops the temporary check that proved family_id NOT NULL above; the column constraint replaces it, and the strict DROP fails the migration if the name is wrong
ALTER TABLE refresh_tokens DROP CONSTRAINT refresh_tokens_family_not_null_chk;

-- Reuse detection and RevokeFamily look up the live tokens of one family.
-- squawk-ignore require-concurrent-index-creation, prefer-robust-stmts -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the index is kept exactly as specified (no IF NOT EXISTS), so a failed run is rolled back and nothing is left to rerun around
CREATE INDEX refresh_tokens_family_live_idx ON refresh_tokens (family_id) WHERE revoked_at IS NULL;

-- A rotated token is always revoked: the rotation sets revoked_at and rotated_at
-- together. The check is added NOT VALID, then validated.
-- squawk-ignore prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_rotated_is_revoked_chk CHECK (rotated_at IS NULL OR revoked_at IS NOT NULL) NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens VALIDATE CONSTRAINT refresh_tokens_rotated_is_revoked_chk;

-- +goose Down
-- squawk-ignore ban-drop-constraint -- rollback of the rotated check added by this migration; no code depends on it after down
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_rotated_is_revoked_chk;
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; rollback of the index added by this migration
DROP INDEX IF EXISTS refresh_tokens_family_live_idx;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; the family and rotation records are not used after down
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS rotated_at;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; the family and rotation records are not used after down
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS family_id;
