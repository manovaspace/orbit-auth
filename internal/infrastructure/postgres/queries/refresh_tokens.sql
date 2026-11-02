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
