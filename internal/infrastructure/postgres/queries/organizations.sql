-- name: CreateOrganization :exec
INSERT INTO organizations (id, name, created_at, updated_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('name')::text,
    sqlc.arg('created_at')::timestamptz,
    sqlc.arg('updated_at')::timestamptz
);

-- name: GetOrganization :one
SELECT id::text AS id, name, created_at, updated_at
FROM organizations
WHERE id = sqlc.arg('id')::uuid;

-- name: ListOrganizationsForUser :many
SELECT o.id::text AS id, o.name, o.created_at, o.updated_at
FROM organizations o
JOIN organization_memberships m ON m.org_id = o.id
WHERE m.user_id = sqlc.arg('user_id')::uuid
  AND m.status = 'active'
ORDER BY o.created_at, o.id;

-- name: AddOrganizationMembership :exec
INSERT INTO organization_memberships (org_id, user_id, status)
VALUES (
    sqlc.arg('org_id')::uuid,
    sqlc.arg('user_id')::uuid,
    sqlc.arg('status')::text
)
ON CONFLICT (org_id, user_id) DO NOTHING;

-- name: LockOrganization :one
SELECT id
FROM organizations
WHERE id = sqlc.arg('id')::uuid
FOR UPDATE;

-- name: UpdateOrganizationMembershipStatus :exec
UPDATE organization_memberships
SET status = sqlc.arg('status')::text,
    updated_at = NOW()
WHERE org_id = sqlc.arg('org_id')::uuid
  AND user_id = sqlc.arg('user_id')::uuid;

-- name: DeleteOrganizationMembership :exec
DELETE FROM organization_memberships
WHERE org_id = sqlc.arg('org_id')::uuid
  AND user_id = sqlc.arg('user_id')::uuid;

-- name: GetOrganizationMembership :one
SELECT org_id::text AS org_id,
       user_id::text AS user_id,
       status,
       created_at,
       updated_at
FROM organization_memberships
WHERE org_id = sqlc.arg('org_id')::uuid
  AND user_id = sqlc.arg('user_id')::uuid;

-- name: CountActiveOrganizationMembers :one
SELECT count(*)::bigint AS count
FROM organization_memberships
WHERE org_id = sqlc.arg('org_id')::uuid
  AND status = 'active';
