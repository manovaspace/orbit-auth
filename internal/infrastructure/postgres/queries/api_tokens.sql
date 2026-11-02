-- name: CreateAPIToken :exec
INSERT INTO api_tokens (id, user_id, name, prefix, token_hash, scopes, expires_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('user_id')::uuid,
    sqlc.arg('name'),
    sqlc.arg('prefix'),
    sqlc.arg('token_hash'),
    sqlc.arg('scopes')::text[],
    sqlc.narg('expires_at')::timestamptz
);

-- name: ListAPITokensByUser :many
SELECT id::text AS id, name, prefix, scopes, expires_at, created_at
FROM api_tokens
WHERE user_id = sqlc.arg('user_id')::uuid AND revoked_at IS NULL
ORDER BY created_at DESC;

-- name: RevokeAPIToken :exec
UPDATE api_tokens SET revoked_at = NOW()
WHERE id = sqlc.arg('id')::uuid AND user_id = sqlc.arg('user_id')::uuid AND revoked_at IS NULL;

-- name: FindAPITokenByPrefix :one
SELECT id::text AS id,
       user_id::text AS user_id,
       name,
       prefix,
       token_hash,
       scopes,
       expires_at,
       revoked_at,
       created_at
FROM api_tokens
WHERE prefix = sqlc.arg('prefix') AND revoked_at IS NULL
ORDER BY created_at DESC
LIMIT 1;
