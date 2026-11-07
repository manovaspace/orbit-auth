-- name: CreateOrganization :exec
INSERT INTO organizations (id, name, slug, created_at, updated_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('name')::text,
    sqlc.arg('slug')::text,
    sqlc.arg('created_at')::timestamptz,
    sqlc.arg('updated_at')::timestamptz
);

-- name: GetOrganization :one
SELECT id::text AS id, name, slug, created_at, updated_at
FROM organizations
WHERE id = sqlc.arg('id')::uuid;

-- name: ListOrganizationsForUser :many
SELECT o.id::text AS id, o.name, o.slug, o.created_at, o.updated_at
FROM organizations o
JOIN organization_memberships m ON m.org_id = o.id
WHERE m.user_id = sqlc.arg('user_id')::uuid
  AND m.status = 'active'
ORDER BY o.created_at, o.id;

-- name: ListOrganizationMembers :many
SELECT org_id::text AS org_id,
       user_id::text AS user_id,
       status,
       role,
       created_at,
       updated_at
FROM organization_memberships
WHERE org_id = sqlc.arg('org_id')::uuid
ORDER BY created_at, user_id;

-- name: AddOrganizationMembership :exec
INSERT INTO organization_memberships (org_id, user_id, status, role)
VALUES (
    sqlc.arg('org_id')::uuid,
    sqlc.arg('user_id')::uuid,
    sqlc.arg('status')::text,
    sqlc.arg('role')::text
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

-- name: UpdateOrganizationMembershipRole :exec
UPDATE organization_memberships
SET role = sqlc.arg('role')::text,
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
       role,
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

-- name: CountActiveOrganizationOwners :one
SELECT count(*)::bigint AS count
FROM organization_memberships
WHERE org_id = sqlc.arg('org_id')::uuid
  AND status = 'active'
  AND role = 'owner';

-- name: DeleteEmptyOrganizations :execrows
DELETE FROM organizations o
WHERE NOT EXISTS (
    SELECT 1 FROM organization_memberships m WHERE m.org_id = o.id
);

-- name: InsertOrganizationSlug :exec
INSERT INTO organization_slugs (slug, org_id)
VALUES (sqlc.arg('slug')::text, sqlc.arg('org_id')::uuid);

-- name: OrganizationSlugExists :one
SELECT EXISTS (SELECT 1 FROM organization_slugs WHERE slug = sqlc.arg('slug')::text) AS found;

-- name: RetireCurrentOrganizationSlug :exec
UPDATE organization_slugs
SET retired_at = NOW()
WHERE org_id = sqlc.arg('org_id')::uuid
  AND retired_at IS NULL;

-- name: ReviveOwnOrganizationSlug :execrows
UPDATE organization_slugs
SET retired_at = NULL
WHERE slug = sqlc.arg('slug')::text
  AND org_id = sqlc.arg('org_id')::uuid;

-- name: UpdateOrganizationSlug :exec
UPDATE organizations
SET slug = sqlc.arg('slug')::text,
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid;

-- name: ResolveOrganizationSlug :one
SELECT o.id::text AS id, o.name, o.slug, o.created_at, o.updated_at
FROM organization_slugs s
JOIN organizations o ON o.id = s.org_id
WHERE s.slug = sqlc.arg('slug')::text;
