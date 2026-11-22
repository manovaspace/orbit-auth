-- name: StoreRefreshToken :exec
INSERT INTO refresh_tokens (id, user_id, org_id, token_hash, expires_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('user_id')::uuid,
    sqlc.narg('org_id')::uuid,
    sqlc.arg('token_hash'),
    sqlc.arg('expires_at')
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
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE token_hash = sqlc.arg('token_hash') AND revoked_at IS NULL AND expires_at > NOW()
RETURNING user_id::text AS user_id, COALESCE(org_id::text, '')::text AS org_id;


-- name: RevokeUserRefreshTokens :execrows
-- Revokes every live refresh token of one account. Disabling an account uses it.
UPDATE refresh_tokens SET revoked_at = NOW()
WHERE user_id = sqlc.arg('user_id')::uuid AND revoked_at IS NULL;

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
