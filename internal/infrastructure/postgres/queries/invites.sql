-- name: RevokePendingOrganizationInvitesForEmail :many
-- Returns the ids it revoked, so the rotation can audit each one.
UPDATE organization_invites
SET status = 'revoked'
WHERE org_id = sqlc.arg('org_id')::uuid
  AND lower(email) = lower(sqlc.arg('email')::text)
  AND status = 'pending'
RETURNING id;

-- name: RevokePendingOrganizationInvitesForPhone :many
-- Returns the ids it revoked, so the rotation can audit each one.
UPDATE organization_invites
SET status = 'revoked'
WHERE org_id = sqlc.arg('org_id')::uuid
  AND phone = sqlc.arg('phone')::text
  AND status = 'pending'
RETURNING id;

-- name: CreateOrganizationInvite :exec
INSERT INTO organization_invites (
    id, org_id, email, phone, role, token_hash, inviter_id, inviter_staff_subject, status, expires_at, created_at
)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('org_id')::uuid,
    sqlc.narg('email')::text,
    sqlc.narg('phone')::text,
    sqlc.arg('role')::text,
    sqlc.arg('token_hash')::text,
    sqlc.narg('inviter_id')::uuid,
    sqlc.narg('inviter_staff_subject')::text,
    sqlc.arg('status')::text,
    sqlc.arg('expires_at')::timestamptz,
    sqlc.arg('created_at')::timestamptz
);

-- name: FindOrganizationInviteByTokenHash :one
SELECT id::text AS id,
       org_id::text AS org_id,
       email,
       phone,
       role,
       token_hash,
       COALESCE(inviter_id::text, '')::text AS inviter_id,
       inviter_staff_subject,
       status,
       expires_at,
       accepted_at,
       created_at
FROM organization_invites
WHERE token_hash = sqlc.arg('token_hash')::text;

-- name: ListOrganizationInvites :many
SELECT id::text AS id,
       org_id::text AS org_id,
       email,
       phone,
       role,
       COALESCE(inviter_id::text, '')::text AS inviter_id,
       inviter_staff_subject,
       status,
       expires_at,
       accepted_at,
       created_at
FROM organization_invites
WHERE org_id = sqlc.arg('org_id')::uuid
ORDER BY created_at DESC, id;

-- name: RevokeOrganizationInvite :execrows
UPDATE organization_invites
SET status = 'revoked'
WHERE id = sqlc.arg('id')::uuid
  AND org_id = sqlc.arg('org_id')::uuid
  AND status = 'pending';

-- name: AcceptOrganizationInvite :execrows
UPDATE organization_invites
SET status = 'accepted',
    accepted_at = sqlc.arg('accepted_at')::timestamptz
WHERE id = sqlc.arg('id')::uuid
  AND org_id = sqlc.arg('org_id')::uuid
  AND status = 'pending';

-- name: AddActiveOrganizationMembership :exec
INSERT INTO organization_memberships (org_id, user_id, status, role)
VALUES (
    sqlc.arg('org_id')::uuid,
    sqlc.arg('user_id')::uuid,
    'active',
    sqlc.arg('role')::text
)
ON CONFLICT (org_id, user_id) DO UPDATE SET
    status = 'active',
    role = EXCLUDED.role,
    updated_at = NOW()
WHERE organization_memberships.status = 'invited';

-- name: GetOrganizationInviteByID :one
-- Reads an invitation without a lock. The caller locks it with
-- LockOrganizationInviteForAccept after it has locked the account and the
-- organization.
SELECT id::text AS id,
       org_id::text AS org_id,
       COALESCE(email, '')::text AS email,
       role,
       status,
       expires_at
FROM organization_invites
WHERE id = sqlc.arg('id')::uuid;

-- name: LockOrganizationInviteForAccept :one
SELECT id::text AS id,
       org_id::text AS org_id,
       COALESCE(email, '')::text AS email,
       role,
       status,
       expires_at
FROM organization_invites
WHERE id = sqlc.arg('id')::uuid
FOR UPDATE;
