-- name: StoreRefreshToken :exec
-- A token with no family_id starts a new family, identified by its own id.
INSERT INTO refresh_tokens (id, user_id, org_id, token_hash, expires_at, family_id)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('user_id')::uuid,
    sqlc.narg('org_id')::uuid,
    sqlc.arg('token_hash'),
    sqlc.arg('expires_at'),
    COALESCE(sqlc.narg('family_id')::uuid, sqlc.arg('id')::uuid)
);

-- name: FindValidRefreshToken :one
SELECT user_id::text AS user_id,
       COALESCE(org_id::text, '')::text AS org_id
FROM refresh_tokens
WHERE token_hash = sqlc.arg('token_hash')
  AND expires_at > NOW()
  AND revoked_at IS NULL;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = sqlc.arg('token_hash');

-- name: ConsumeRefreshToken :one
-- Revokes a live token. rotate is true for a rotation (the token is marked
-- rotated, so a later presentation is detected as reuse) and false for a logout.
UPDATE refresh_tokens SET revoked_at = NOW(),
    rotated_at = CASE WHEN sqlc.arg('rotate')::boolean THEN NOW() ELSE NULL END
WHERE token_hash = sqlc.arg('token_hash') AND revoked_at IS NULL AND expires_at > NOW()
RETURNING user_id::text AS user_id, COALESCE(org_id::text, '')::text AS org_id, family_id::text AS family_id;

-- name: FindRotatedRefreshToken :one
-- Finds a token that a rotation consumed. A reuse of it means the token was copied.
SELECT user_id::text AS user_id, family_id::text AS family_id, rotated_at
FROM refresh_tokens
WHERE token_hash = sqlc.arg('token_hash') AND rotated_at IS NOT NULL;

-- name: RevokeRefreshTokenFamily :execrows
-- Revokes every live token of one family. Reuse of a rotated token uses it.
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE family_id = sqlc.arg('family_id')::uuid AND revoked_at IS NULL;


-- name: RevokeUserRefreshTokens :execrows
-- Revokes every live refresh token of one account. Disabling an account uses it.
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE user_id = sqlc.arg('user_id')::uuid AND revoked_at IS NULL;

-- name: RevokeUserRefreshTokensExcept :execrows
-- Revokes every live refresh token of one account except keep_hash. An empty
-- keep_hash keeps none, because no token hash is empty.
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE user_id = sqlc.arg('user_id')::uuid
  AND revoked_at IS NULL
  AND token_hash <> sqlc.arg('keep_hash')::text;

-- name: RevokeOrganizationRefreshTokens :execrows
-- Revokes the live refresh tokens of one account that selected one organization.
-- Suspending or removing that membership uses it; other organizations are kept.
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE user_id = sqlc.arg('user_id')::uuid
  AND org_id = sqlc.arg('org_id')::uuid
  AND revoked_at IS NULL;

-- name: LockUserForShare :one
-- Takes a share lock on the account row for the sign-in transaction that stores a
-- refresh token. DisableUser takes the conflicting FOR UPDATE lock, so a token
-- stored by a concurrent sign-in either commits first and is revoked by the
-- disable, or the disable commits first and the sign-in finds the account
-- disabled.
SELECT status FROM users WHERE id = sqlc.arg('id')::uuid FOR SHARE;
