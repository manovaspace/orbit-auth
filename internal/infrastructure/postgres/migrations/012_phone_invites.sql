-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- An invitation targets exactly one recipient: an email address or an E.164
-- phone number (ADR-061). Email becomes nullable and phone is added; the check
-- below keeps exactly one of them set. Existing rows all carry an email, so
-- they satisfy the check unchanged.
ALTER TABLE organization_invites ALTER COLUMN email DROP NOT NULL;
ALTER TABLE organization_invites ADD COLUMN IF NOT EXISTS phone TEXT;

-- The organization tier the invitee receives on acceptance. Existing invites
-- were always plain member invitations.
ALTER TABLE organization_invites
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member'
        CHECK (role IN ('owner', 'admin', 'member'));

ALTER TABLE organization_invites
    ADD CONSTRAINT organization_invites_one_target_chk
        CHECK ((email IS NULL) <> (phone IS NULL));

-- The pending-email index only applies to email invitations. NULLs never
-- collide in a unique index, so this is clarity rather than a behaviour change.
DROP INDEX IF EXISTS organization_invites_pending_email_idx;
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_email_idx
    ON organization_invites (org_id, lower(email))
    WHERE status = 'pending' AND email IS NOT NULL;

-- At most one pending invitation per organization and phone number.
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_phone_idx
    ON organization_invites (org_id, phone)
    WHERE status = 'pending' AND phone IS NOT NULL;

-- +goose Down
-- Phone invitations cannot be represented in the email-only shape restored
-- below, so they are deleted first. Accepted phone invitations are removed too;
-- the memberships they created remain in organization_memberships. Deleting
-- must run before email is made NOT NULL again.
DELETE FROM organization_invites WHERE email IS NULL;

DROP INDEX IF EXISTS organization_invites_pending_phone_idx;
DROP INDEX IF EXISTS organization_invites_pending_email_idx;
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_email_idx
    ON organization_invites (org_id, lower(email))
    WHERE status = 'pending';

ALTER TABLE organization_invites DROP CONSTRAINT IF EXISTS organization_invites_one_target_chk;
ALTER TABLE organization_invites DROP COLUMN IF EXISTS role;
ALTER TABLE organization_invites DROP COLUMN IF EXISTS phone;
ALTER TABLE organization_invites ALTER COLUMN email SET NOT NULL;
