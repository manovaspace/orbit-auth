-- name: FindUserByEmail :one
SELECT id::text AS id,
       COALESCE(email, '') AS email,
       COALESCE(mobile, '') AS mobile,
       COALESCE(password_hash, '') AS password_hash,
       COALESCE(display_name, '') AS display_name,
       COALESCE(status, 'active') AS status,
       (email_verified_at IS NOT NULL)::boolean AS email_verified,
       (mobile_verified_at IS NOT NULL)::boolean AS mobile_verified
FROM users
WHERE email = sqlc.arg('email')::text;

-- name: FindUserByMobile :one
SELECT id::text AS id,
       COALESCE(email, '') AS email,
       COALESCE(mobile, '') AS mobile,
       COALESCE(password_hash, '') AS password_hash,
       COALESCE(display_name, '') AS display_name,
       COALESCE(status, 'active') AS status,
       (email_verified_at IS NOT NULL)::boolean AS email_verified,
       (mobile_verified_at IS NOT NULL)::boolean AS mobile_verified
FROM users
WHERE mobile = sqlc.arg('mobile')::text;

-- name: FindUserByID :one
SELECT id::text AS id,
       COALESCE(email, '') AS email,
       COALESCE(mobile, '') AS mobile,
       COALESCE(password_hash, '') AS password_hash,
       COALESCE(display_name, '') AS display_name,
       COALESCE(status, 'active') AS status,
       (email_verified_at IS NOT NULL)::boolean AS email_verified,
       (mobile_verified_at IS NOT NULL)::boolean AS mobile_verified
FROM users
WHERE id = sqlc.arg('id')::uuid;

-- name: CreateUserWithEmail :exec
INSERT INTO users (id, email, status, email_verified_at)
VALUES (sqlc.arg('id')::uuid, sqlc.arg('email')::text, sqlc.arg('status')::text, NOW());

-- name: CreateUserWithMobile :exec
INSERT INTO users (id, mobile)
VALUES (sqlc.arg('id')::uuid, sqlc.arg('mobile')::text);

-- name: UpdateLastLogin :exec
UPDATE users SET last_login_at = NOW(), updated_at = NOW() WHERE id = sqlc.arg('id')::uuid;

-- name: CreateSignupUser :execrows
INSERT INTO users (id, email, password_hash, display_name, status, email_verified_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('email')::text,
    sqlc.arg('password_hash')::text,
    sqlc.arg('display_name')::text,
    sqlc.arg('status')::text,
    sqlc.narg('email_verified_at')::timestamptz
)
ON CONFLICT (email) DO NOTHING;

-- name: ReissueSignupUser :one
UPDATE users
SET password_hash = sqlc.arg('password_hash')::text,
    display_name = sqlc.arg('display_name')::text,
    updated_at = NOW()
WHERE email = sqlc.arg('email')::text
  AND status = 'signup_pending'
  AND email_verified_at IS NULL
RETURNING id::text AS id;

-- name: VerifySignupUser :execrows
UPDATE users
SET status = 'active',
    email_verified_at = sqlc.arg('email_verified_at')::timestamptz,
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid
  AND status = 'signup_pending'
  AND email_verified_at IS NULL;

-- name: PurgeUnverifiedSignups :execrows
DELETE FROM users
WHERE status = 'signup_pending'
  AND email_verified_at IS NULL
  AND created_at < sqlc.arg('before')::timestamptz;

-- name: UpsertDemoUserByEmail :exec
INSERT INTO users (id, email, mobile, password_hash, display_name, status, email_verified_at, mobile_verified_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.narg('email')::text,
    sqlc.narg('mobile')::text,
    sqlc.arg('password_hash')::text,
    sqlc.arg('display_name')::text,
    'active',
    CASE WHEN sqlc.narg('email')::text IS NOT NULL THEN NOW() ELSE NULL END,
    CASE WHEN sqlc.narg('mobile')::text IS NOT NULL THEN NOW() ELSE NULL END
)
ON CONFLICT (email) DO UPDATE SET
    email = COALESCE(EXCLUDED.email, users.email),
    mobile = COALESCE(EXCLUDED.mobile, users.mobile),
    password_hash = EXCLUDED.password_hash,
    display_name = EXCLUDED.display_name,
    status = 'active',
    email_verified_at = CASE
        WHEN EXCLUDED.email IS NOT NULL THEN COALESCE(users.email_verified_at, NOW())
        ELSE users.email_verified_at
    END,
    mobile_verified_at = CASE
        WHEN EXCLUDED.mobile IS NOT NULL THEN COALESCE(users.mobile_verified_at, NOW())
        ELSE users.mobile_verified_at
    END,
    updated_at = NOW();

-- name: UpsertDemoUserByMobile :exec
INSERT INTO users (id, email, mobile, password_hash, display_name, status, email_verified_at, mobile_verified_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.narg('email')::text,
    sqlc.narg('mobile')::text,
    sqlc.arg('password_hash')::text,
    sqlc.arg('display_name')::text,
    'active',
    CASE WHEN sqlc.narg('email')::text IS NOT NULL THEN NOW() ELSE NULL END,
    CASE WHEN sqlc.narg('mobile')::text IS NOT NULL THEN NOW() ELSE NULL END
)
ON CONFLICT (mobile) DO UPDATE SET
    email = COALESCE(EXCLUDED.email, users.email),
    mobile = COALESCE(EXCLUDED.mobile, users.mobile),
    password_hash = EXCLUDED.password_hash,
    display_name = EXCLUDED.display_name,
    status = 'active',
    email_verified_at = CASE
        WHEN EXCLUDED.email IS NOT NULL THEN COALESCE(users.email_verified_at, NOW())
        ELSE users.email_verified_at
    END,
    mobile_verified_at = CASE
        WHEN EXCLUDED.mobile IS NOT NULL THEN COALESCE(users.mobile_verified_at, NOW())
        ELSE users.mobile_verified_at
    END,
    updated_at = NOW();

-- name: CreatePhoneSignupUser :execrows
INSERT INTO users (id, mobile, password_hash, display_name, status)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('mobile')::text,
    sqlc.arg('password_hash')::text,
    sqlc.arg('display_name')::text,
    sqlc.arg('status')::text
)
ON CONFLICT (mobile) DO NOTHING;

-- name: MarkMobileVerified :execrows
UPDATE users
SET mobile_verified_at = COALESCE(mobile_verified_at, sqlc.arg('verified_at')::timestamptz),
    updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid;
