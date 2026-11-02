-- name: FindUserByEmail :one
SELECT id::text AS id,
       COALESCE(email, '') AS email,
       COALESCE(mobile, '') AS mobile,
       COALESCE(password_hash, '') AS password_hash,
       COALESCE(display_name, '') AS display_name,
       COALESCE(status, 'active') AS status
FROM users
WHERE email = sqlc.arg('email')::text;

-- name: FindUserByMobile :one
SELECT id::text AS id,
       COALESCE(email, '') AS email,
       COALESCE(mobile, '') AS mobile,
       COALESCE(password_hash, '') AS password_hash,
       COALESCE(display_name, '') AS display_name,
       COALESCE(status, 'active') AS status
FROM users
WHERE mobile = sqlc.arg('mobile')::text;

-- name: CreateUserWithEmail :exec
INSERT INTO users (id, email, status)
VALUES (sqlc.arg('id')::uuid, sqlc.arg('email')::text, sqlc.arg('status')::text);

-- name: CreateUserWithMobile :exec
INSERT INTO users (id, mobile)
VALUES (sqlc.arg('id')::uuid, sqlc.arg('mobile')::text);

-- name: UpdateLastLogin :exec
UPDATE users SET last_login_at = NOW(), updated_at = NOW() WHERE id = sqlc.arg('id')::uuid;

-- name: UpsertDemoUserByEmail :exec
INSERT INTO users (id, email, mobile, password_hash, display_name, status)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.narg('email')::text,
    sqlc.narg('mobile')::text,
    sqlc.arg('password_hash')::text,
    sqlc.arg('display_name')::text,
    'active'
)
ON CONFLICT (email) DO UPDATE SET
    email = COALESCE(EXCLUDED.email, users.email),
    mobile = COALESCE(EXCLUDED.mobile, users.mobile),
    password_hash = EXCLUDED.password_hash,
    display_name = EXCLUDED.display_name,
    status = 'active',
    updated_at = NOW();

-- name: UpsertDemoUserByMobile :exec
INSERT INTO users (id, email, mobile, password_hash, display_name, status)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.narg('email')::text,
    sqlc.narg('mobile')::text,
    sqlc.arg('password_hash')::text,
    sqlc.arg('display_name')::text,
    'active'
)
ON CONFLICT (mobile) DO UPDATE SET
    email = COALESCE(EXCLUDED.email, users.email),
    mobile = COALESCE(EXCLUDED.mobile, users.mobile),
    password_hash = EXCLUDED.password_hash,
    display_name = EXCLUDED.display_name,
    status = 'active',
    updated_at = NOW();
