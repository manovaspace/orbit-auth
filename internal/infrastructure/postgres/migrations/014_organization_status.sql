-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Organization status for the staff console (ADR-061). A suspended organization
-- cannot be selected into or refreshed into; its members keep their
-- memberships and the organization is still listed and resolvable. The check
-- pins the two values; the transition rules are code.
-- squawk-ignore adding-not-nullable-field, prefer-robust-stmts -- the column has a constant default, so Postgres 11+ adds it without rewriting the table; the table holds one row per organization
ALTER TABLE organizations
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended'));
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;
-- squawk-ignore adding-not-nullable-field, prefer-robust-stmts -- a constant default ('') keeps the column cheap to add; the table holds one row per organization
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS suspended_reason TEXT NOT NULL DEFAULT '';

-- Staff invitations have no user as their inviter. inviter_id becomes nullable
-- and inviter_staff_subject records the staff identity instead. Exactly one of
-- the two is set, and a staff subject is never empty. Existing rows all carry
-- inviter_id and no staff subject, so they satisfy the check unchanged.
-- squawk-ignore ban-drop-not-null, prefer-robust-stmts -- staff-issued invitations have no user inviter; the check below keeps exactly one inviter set
ALTER TABLE organization_invites ALTER COLUMN inviter_id DROP NOT NULL;
ALTER TABLE organization_invites ADD COLUMN IF NOT EXISTS inviter_staff_subject TEXT;
-- squawk-ignore constraint-missing-not-valid, prefer-robust-stmts -- the table is small, every existing row satisfies the check, and goose runs this file in one transaction
ALTER TABLE organization_invites ADD CONSTRAINT organization_invites_one_inviter_chk CHECK (
    ((inviter_id IS NULL) <> (inviter_staff_subject IS NULL))
    AND (inviter_staff_subject IS NULL OR inviter_staff_subject <> '')
);

-- Idempotency keys of ProvisionOrganization. A key maps to the one organization
-- it created and to the owner target that organization was provisioned for, so
-- a repeat with another name or owner target is a conflict. The organization row
-- is removed with its key. The check keeps exactly one owner target set.
CREATE TABLE IF NOT EXISTS organization_provisions (
    idempotency_key TEXT PRIMARY KEY CHECK (idempotency_key <> ''),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    owner_email TEXT,
    owner_phone TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT organization_provisions_one_owner_chk CHECK ((owner_email IS NULL) <> (owner_phone IS NULL))
);

-- +goose Down
-- squawk-ignore ban-drop-table -- rollback of the idempotency table added by this migration; no code reads it after down
DROP TABLE IF EXISTS organization_provisions;
-- Staff-issued invitations cannot be represented in the user-inviter shape
-- restored below, so they are deleted first. The memberships they created, if
-- any, remain.
DELETE FROM organization_invites WHERE inviter_id IS NULL;
-- squawk-ignore ban-drop-constraint -- rollback of the one-inviter check added by this migration; no client depends on it after down
ALTER TABLE organization_invites DROP CONSTRAINT IF EXISTS organization_invites_one_inviter_chk;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE organization_invites DROP COLUMN IF EXISTS inviter_staff_subject;
-- squawk-ignore prefer-robust-stmts, adding-not-nullable-field -- the DELETE above removed every row with a NULL inviter, so no NULL can remain
ALTER TABLE organization_invites ALTER COLUMN inviter_id SET NOT NULL;
-- Suspensions are not kept after down: the organizations become active again.
-- squawk-ignore ban-drop-column -- rollback of the columns added by this migration; no code reads them after down
ALTER TABLE organizations DROP COLUMN IF EXISTS suspended_reason;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE organizations DROP COLUMN IF EXISTS suspended_at;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE organizations DROP COLUMN IF EXISTS status;
