-- name: ListUsersForStaff :many
-- One page of accounts for the staff console, oldest first. The keyset
-- (created_at, id) resumes after the previous page; has_after false starts at the
-- beginning. An empty status_filter or pattern matches every row. pattern is a
-- complete ILIKE pattern built by the adapter, which escapes the user's text. The
-- row never selects a password hash or a token.
SELECT u.id::text AS id,
       COALESCE(u.email, '')::text AS email,
       COALESCE(u.mobile, '')::text AS mobile,
       COALESCE(u.username, '')::text AS username,
       COALESCE(u.display_name, '')::text AS display_name,
       u.status,
       (u.email_verified_at IS NOT NULL)::boolean AS email_verified,
       (u.mobile_verified_at IS NOT NULL)::boolean AS mobile_verified,
       (COALESCE(u.password_hash, '') <> '')::boolean AS has_password,
       EXISTS (SELECT 1 FROM user_identities i WHERE i.user_id = u.id)::boolean AS google_linked,
       u.created_at,
       u.last_login_at,
       u.disabled_at,
       u.disabled_reason,
       (SELECT count(*)::bigint FROM organization_memberships m WHERE m.user_id = u.id) AS membership_count
FROM users u
WHERE (sqlc.arg('status_filter')::text = '' OR u.status = sqlc.arg('status_filter')::text)
  AND (sqlc.arg('pattern')::text = ''
       OR u.email ILIKE sqlc.arg('pattern')::text
       OR u.mobile ILIKE sqlc.arg('pattern')::text
       OR u.username ILIKE sqlc.arg('pattern')::text
       OR u.display_name ILIKE sqlc.arg('pattern')::text)
  AND (NOT sqlc.arg('has_after')::bool OR (u.created_at, u.id) > (sqlc.arg('after_created_at')::timestamptz, sqlc.arg('after_id')::uuid))
ORDER BY u.created_at, u.id
LIMIT sqlc.arg('row_limit')::int;

-- name: GetUserForStaff :one
SELECT u.id::text AS id,
       COALESCE(u.email, '')::text AS email,
       COALESCE(u.mobile, '')::text AS mobile,
       COALESCE(u.username, '')::text AS username,
       COALESCE(u.display_name, '')::text AS display_name,
       u.status,
       (u.email_verified_at IS NOT NULL)::boolean AS email_verified,
       (u.mobile_verified_at IS NOT NULL)::boolean AS mobile_verified,
       (COALESCE(u.password_hash, '') <> '')::boolean AS has_password,
       EXISTS (SELECT 1 FROM user_identities i WHERE i.user_id = u.id)::boolean AS google_linked,
       u.created_at,
       u.last_login_at,
       u.disabled_at,
       u.disabled_reason,
       (SELECT count(*)::bigint FROM organization_memberships m WHERE m.user_id = u.id) AS membership_count
FROM users u
WHERE u.id = sqlc.arg('id')::uuid;

-- name: ListMembershipsForStaffUser :many
SELECT m.org_id::text AS org_id,
       o.name AS org_name,
       o.slug AS org_slug,
       o.status AS org_status,
       m.role,
       m.status,
       m.created_at AS joined_at
FROM organization_memberships m
JOIN organizations o ON o.id = m.org_id
WHERE m.user_id = sqlc.arg('user_id')::uuid
ORDER BY m.created_at, m.org_id;

-- name: LockUserForUpdate :one
-- Locks the account row for a staff disable or enable. It conflicts with the
-- share lock that sign-in takes before storing a refresh token.
SELECT status FROM users WHERE id = sqlc.arg('id')::uuid FOR UPDATE;

-- name: SetUserDisabled :execrows
UPDATE users
SET status = 'disabled',
    disabled_at = NOW(),
    disabled_reason = sqlc.arg('reason')::text,
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid;

-- name: SetUserEnabled :execrows
UPDATE users
SET status = 'active',
    disabled_at = NULL,
    disabled_reason = '',
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid;
