-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- An invitation targets exactly one recipient: an email address or an E.164
-- phone number (ADR-061). Email becomes nullable and phone is added; the check
-- below keeps exactly one of them set. Existing rows all carry an email, so
-- they satisfy the check unchanged.
-- squawk-ignore prefer-robust-stmts, ban-drop-not-null -- email is nullable on purpose: a phone invite has no email. Readers map NULL to "" (postgres.textValue) and the domain checks the phone first, so no reader needs an email
ALTER TABLE organization_invites ALTER COLUMN email DROP NOT NULL;
ALTER TABLE organization_invites ADD COLUMN IF NOT EXISTS phone TEXT;

-- The organization tier the invitee receives on acceptance. Existing invites
-- were always plain member invitations.
ALTER TABLE organization_invites
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member'
        CHECK (role IN ('owner', 'admin', 'member'));

-- The check is validated now: the table is small and every existing row has an
-- email and no phone, so it passes. goose runs this file in one transaction.
-- squawk-ignore constraint-missing-not-valid, prefer-robust-stmts -- the table is small, every existing row satisfies the check, and goose runs this file in one transaction
ALTER TABLE organization_invites ADD CONSTRAINT organization_invites_one_target_chk CHECK ((email IS NULL) <> (phone IS NULL));

-- The pending-email index only applies to email invitations. NULLs never
-- collide in a unique index, so this is clarity rather than a behaviour change.
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table is small
DROP INDEX IF EXISTS organization_invites_pending_email_idx;
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table is small
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_email_idx
    ON organization_invites (org_id, lower(email))
    WHERE status = 'pending' AND email IS NOT NULL;

-- At most one pending invitation per organization and phone number.
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table is small
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_phone_idx
    ON organization_invites (org_id, phone)
    WHERE status = 'pending' AND phone IS NOT NULL;

-- +goose Down
-- Phone invitations cannot be represented in the email-only shape restored
-- below, so they are deleted first. Accepted phone invitations are removed too;
-- the memberships they created remain in organization_memberships. Deleting
-- must run before email is made NOT NULL again.
DELETE FROM organization_invites WHERE email IS NULL;

-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table is small
DROP INDEX IF EXISTS organization_invites_pending_phone_idx;
-- squawk-ignore require-concurrent-index-deletion -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table is small
DROP INDEX IF EXISTS organization_invites_pending_email_idx;
-- squawk-ignore require-concurrent-index-creation -- goose runs this file in one transaction, where CONCURRENTLY cannot run; the table is small
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_email_idx
    ON organization_invites (org_id, lower(email))
    WHERE status = 'pending';

-- Rolling back the check and the added columns is the reverse of the Up section.
-- squawk-ignore ban-drop-constraint -- rollback of the one-target check added by this migration; no client depends on it after down
ALTER TABLE organization_invites DROP CONSTRAINT IF EXISTS organization_invites_one_target_chk;
-- squawk-ignore ban-drop-column -- rollback of the role column added by this migration; no code reads it after down
ALTER TABLE organization_invites DROP COLUMN IF EXISTS role;
-- squawk-ignore ban-drop-column -- rollback of the phone column added by this migration; no code reads it after down
ALTER TABLE organization_invites DROP COLUMN IF EXISTS phone;
-- squawk-ignore prefer-robust-stmts, adding-not-nullable-field -- the DELETE above removed every row with a NULL email, so no NULL can remain
ALTER TABLE organization_invites ALTER COLUMN email SET NOT NULL;
