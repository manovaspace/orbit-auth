-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Organization administration tier. orbit-auth does not model product roles
-- (ADR-045 D1), but it must know who administers an organization so that
-- membership and invite writes can be authorized where the data lives.
ALTER TABLE organization_memberships
    ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member'
        CHECK (role IN ('owner', 'admin', 'member'));

-- Backfill: every organization that has an active member gets exactly one
-- owner, the earliest active membership (the creator in practice). Everyone
-- else stays a plain member and an owner can promote them afterwards.
UPDATE organization_memberships m
SET role = 'owner'
FROM (
    SELECT DISTINCT ON (org_id) org_id, user_id
    FROM organization_memberships
    WHERE status = 'active'
    ORDER BY org_id, created_at, user_id
) first_active
WHERE m.org_id = first_active.org_id
  AND m.user_id = first_active.user_id;

-- +goose Down
ALTER TABLE organization_memberships DROP COLUMN role;
