-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Hard deletes must not leave rows that point at nothing. PurgeUnverifiedSignups
-- deletes accounts and DeleteEmptyOrganizations deletes organizations. Each new
-- or changed foreign key is added NOT VALID and validated before anything it
-- replaces is dropped, so the rule is never unenforced.

-- A refresh token belongs to one account. Removing the account removes its
-- tokens. Before this migration no foreign key existed, so a token can outlive
-- its account; such a database fails the VALIDATE below and must be cleaned first.
-- squawk-ignore adding-foreign-key-constraint, prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens VALIDATE CONSTRAINT refresh_tokens_user_id_fkey;

-- A refresh token may carry the organization it selected (org_id is nullable).
-- Removing the organization removes the tokens that selected it; without the
-- cascade, DeleteEmptyOrganizations would fail on this foreign key.
-- squawk-ignore adding-foreign-key-constraint, prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE refresh_tokens ADD CONSTRAINT refresh_tokens_org_id_fkey FOREIGN KEY (org_id) REFERENCES organizations(id) ON DELETE CASCADE NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE refresh_tokens VALIDATE CONSTRAINT refresh_tokens_org_id_fkey;

-- An API token belongs to one account, and removing the account removes its
-- tokens. The replacement has a new name because PostgreSQL does not allow two
-- constraints with one name on one table; the Down section restores the original
-- name. api_tokens_user_idx (user_id, created_at DESC, migration 003) leads with
-- user_id, so it already indexes the foreign key and no index is added.
-- squawk-ignore adding-foreign-key-constraint, prefer-robust-stmts -- the constraint is added NOT VALID, so the scan is deferred to VALIDATE CONSTRAINT; goose runs this file in one transaction
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_user_id_cascade_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
-- squawk-ignore prefer-robust-stmts -- goose runs this file in one transaction, so a failed run leaves nothing behind
ALTER TABLE api_tokens VALIDATE CONSTRAINT api_tokens_user_id_cascade_fkey;
-- squawk-ignore ban-drop-constraint, prefer-robust-stmts -- the replaced foreign key (no ON DELETE action) is superseded by api_tokens_user_id_cascade_fkey validated above; the DROP is strict on purpose, so a wrong name fails the migration instead of passing silently
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_user_id_fkey;

-- +goose Down
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) NOT VALID;
ALTER TABLE api_tokens VALIDATE CONSTRAINT api_tokens_user_id_fkey;
-- squawk-ignore ban-drop-constraint -- rollback of the cascade foreign key added by this migration; no code depends on it after down
ALTER TABLE api_tokens DROP CONSTRAINT IF EXISTS api_tokens_user_id_cascade_fkey;

-- squawk-ignore ban-drop-constraint -- rollback of the foreign key added by this migration; no code depends on it after down
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_org_id_fkey;
-- squawk-ignore ban-drop-constraint -- rollback of the foreign key added by this migration; no code depends on it after down
ALTER TABLE refresh_tokens DROP CONSTRAINT IF EXISTS refresh_tokens_user_id_fkey;
