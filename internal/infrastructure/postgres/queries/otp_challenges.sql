-- name: UpsertChallenge :exec
INSERT INTO otp_challenges (id, identifier, channel, code_hash, expires_at, attempts)
VALUES (sqlc.arg('id')::uuid, sqlc.arg('identifier'), sqlc.arg('channel'), sqlc.arg('code_hash'), sqlc.arg('expires_at'), 0);

-- name: GetLatestChallenge :one
SELECT id::text AS id, identifier, channel, code_hash, expires_at, attempts
FROM otp_challenges
WHERE identifier = sqlc.arg('identifier') AND channel = sqlc.arg('channel')
ORDER BY created_at DESC
LIMIT 1;

-- name: IncrementChallengeAttempts :exec
UPDATE otp_challenges SET attempts = attempts + 1 WHERE id = sqlc.arg('id')::uuid;

-- name: ConsumeChallenge :exec
DELETE FROM otp_challenges WHERE id = sqlc.arg('id')::uuid;

-- name: DeleteExpiredChallenges :exec
DELETE FROM otp_challenges WHERE expires_at < NOW();
