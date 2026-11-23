-- name: UpsertChallenge :exec
INSERT INTO identity.otp_challenges (id, identifier, channel, purpose, code_hash, expires_at, attempts, created_at)
VALUES (
    sqlc.arg('id')::uuid,
    sqlc.arg('identifier'),
    sqlc.arg('channel'),
    sqlc.arg('purpose'),
    sqlc.arg('code_hash'),
    sqlc.arg('expires_at'),
    0,
    sqlc.arg('created_at')::timestamptz
);

-- name: GetLatestChallenge :one
SELECT id::text AS id, identifier, channel, purpose, code_hash, expires_at, created_at, attempts
FROM identity.otp_challenges
WHERE identifier = sqlc.arg('identifier')
  AND channel = sqlc.arg('channel')
  AND purpose = sqlc.arg('purpose')
ORDER BY created_at DESC
LIMIT 1;

-- name: ConsumeChallengeAttempt :one
UPDATE identity.otp_challenges SET attempts = attempts + 1
WHERE id = sqlc.arg('id')::uuid AND attempts < sqlc.arg('max_attempts')::int
RETURNING attempts;

-- name: ConsumeChallenge :execrows
DELETE FROM identity.otp_challenges WHERE id = sqlc.arg('id')::uuid;

-- name: DeleteChallengesForIdentifier :exec
DELETE FROM identity.otp_challenges
WHERE identifier = sqlc.arg('identifier')
  AND channel = sqlc.arg('channel')
  AND purpose = sqlc.arg('purpose');

-- name: DeleteExpiredChallenges :exec
DELETE FROM identity.otp_challenges WHERE expires_at < NOW();
