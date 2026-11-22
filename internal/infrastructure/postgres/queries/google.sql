-- name: CreateOAuthSignInState :exec
INSERT INTO oauth_sign_in_states (state_hash, redirect_uri, return_to, expires_at)
VALUES (
    sqlc.arg('state_hash')::text,
    sqlc.arg('redirect_uri')::text,
    sqlc.arg('return_to')::text,
    sqlc.arg('expires_at')::timestamptz
);

-- name: ConsumeOAuthSignInState :one
DELETE FROM oauth_sign_in_states
WHERE state_hash = sqlc.arg('state_hash')::text
  AND expires_at > sqlc.arg('now')::timestamptz
RETURNING redirect_uri, return_to;

-- name: DeleteExpiredOAuthSignInStates :execrows
DELETE FROM oauth_sign_in_states
WHERE expires_at <= sqlc.arg('now')::timestamptz;

-- name: UpdateGoogleIdentityLogin :one
UPDATE user_identities
SET email = COALESCE(sqlc.narg('email')::text, email),
    last_login_at = NOW()
WHERE provider = sqlc.arg('provider')::text
  AND subject = sqlc.arg('subject')::text
RETURNING user_id::text AS user_id;

-- name: LockUserByEmail :many
SELECT id::text AS id,
       (email_verified_at IS NOT NULL)::boolean AS email_verified
FROM users
WHERE lower(email) = sqlc.arg('email')::text
ORDER BY id
FOR UPDATE;

-- name: HasIdentityForProvider :one
SELECT EXISTS (
    SELECT 1 FROM user_identities
    WHERE provider = sqlc.arg('provider')::text
      AND user_id = sqlc.arg('user_id')::uuid
)::boolean AS linked;

-- name: InsertUserIdentity :exec
INSERT INTO user_identities (provider, subject, user_id, email)
VALUES (
    sqlc.arg('provider')::text,
    sqlc.arg('subject')::text,
    sqlc.arg('user_id')::uuid,
    sqlc.narg('email')::text
);

-- name: CreateGoogleUser :exec
INSERT INTO users (id, email, display_name, status, email_verified_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.narg('email')::text,
    sqlc.arg('display_name')::text,
    'active',
    CASE WHEN sqlc.narg('email')::text IS NULL THEN NULL ELSE NOW() END
);
