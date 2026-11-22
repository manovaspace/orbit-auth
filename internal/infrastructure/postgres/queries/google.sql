-- name: CreateOAuthSignInState :exec
INSERT INTO oauth_sign_in_states (state_hash, redirect_uri, return_to, expires_at, purpose, invite_id, user_id)
VALUES (
    sqlc.arg('state_hash')::text,
    sqlc.arg('redirect_uri')::text,
    sqlc.arg('return_to')::text,
    sqlc.arg('expires_at')::timestamptz,
    sqlc.arg('purpose')::text,
    sqlc.narg('invite_id')::uuid,
    sqlc.narg('user_id')::uuid
);

-- name: ConsumeOAuthSignInState :one
DELETE FROM oauth_sign_in_states
WHERE state_hash = sqlc.arg('state_hash')::text
  AND expires_at > sqlc.arg('now')::timestamptz
RETURNING redirect_uri,
          return_to,
          purpose,
          COALESCE(invite_id::text, '')::text AS invite_id,
          COALESCE(user_id::text, '')::text AS user_id;

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

-- name: FindGoogleIdentityUser :one
-- The account a Google subject is linked to. No row is ErrNoRows.
SELECT user_id::text AS user_id
FROM user_identities
WHERE provider = sqlc.arg('provider')::text
  AND subject = sqlc.arg('subject')::text;

-- name: FindGoogleIdentityForUser :one
-- The Google identity linked to one account, without its subject.
SELECT email, created_at, last_login_at
FROM user_identities
WHERE provider = sqlc.arg('provider')::text
  AND user_id = sqlc.arg('user_id')::uuid;

-- name: DeleteGoogleIdentityForUser :execrows
DELETE FROM user_identities
WHERE provider = sqlc.arg('provider')::text
  AND user_id = sqlc.arg('user_id')::uuid;

-- name: ActivatePendingUserForGoogle :execrows
-- Takes over a signup_pending account for a verified Google email. The
-- password is cleared, the display name is replaced when Google gave one, and
-- the email is marked verified now.
UPDATE users
SET status = 'active',
    email_verified_at = NOW(),
    password_hash = NULL,
    display_name = CASE WHEN sqlc.arg('display_name')::text = '' THEN display_name ELSE sqlc.arg('display_name')::text END,
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid
  AND status = 'signup_pending';

-- name: SetUserPasswordHash :execrows
-- Stores a new password hash for an active account.
UPDATE users
SET password_hash = sqlc.arg('password_hash')::text,
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid
  AND status = 'active';
