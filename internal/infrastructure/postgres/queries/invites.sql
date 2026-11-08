-- name: RevokePendingOrganizationInvitesForEmail :exec
UPDATE organization_invites
SET status = 'revoked'
WHERE org_id = sqlc.arg('org_id')::uuid
  AND lower(email) = lower(sqlc.arg('email')::text)
  AND status = 'pending';

-- name: RevokePendingOrganizationInvitesForPhone :exec
UPDATE organization_invites
SET status = 'revoked'
WHERE org_id = sqlc.arg('org_id')::uuid
  AND phone = sqlc.arg('phone')::text
  AND status = 'pending';

-- name: CreateOrganizationInvite :exec
INSERT INTO organization_invites (
    id, org_id, email, phone, role, token_hash, inviter_id, status, expires_at, created_at
)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('org_id')::uuid,
    sqlc.narg('email')::text,
    sqlc.narg('phone')::text,
    sqlc.arg('role')::text,
    sqlc.arg('token_hash')::text,
    sqlc.arg('inviter_id')::uuid,
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
       inviter_id::text AS inviter_id,
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
       inviter_id::text AS inviter_id,
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
