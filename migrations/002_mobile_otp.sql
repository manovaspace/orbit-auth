-- +goose Up
ALTER TABLE otp_challenges RENAME COLUMN email TO identifier;
ALTER TABLE otp_challenges ADD COLUMN IF NOT EXISTS channel TEXT NOT NULL DEFAULT 'email';
DROP INDEX IF EXISTS otp_challenges_email_idx;
CREATE INDEX IF NOT EXISTS otp_challenges_identifier_idx ON otp_challenges (identifier, channel, created_at DESC);

ALTER TABLE users ADD COLUMN IF NOT EXISTS mobile TEXT UNIQUE;
ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS users_mobile_idx ON users (mobile) WHERE mobile IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS users_mobile_idx;
ALTER TABLE users DROP COLUMN IF EXISTS mobile;
DROP INDEX IF EXISTS otp_challenges_identifier_idx;
ALTER TABLE otp_challenges DROP COLUMN IF EXISTS channel;
ALTER TABLE otp_challenges RENAME COLUMN identifier TO email;
CREATE INDEX IF NOT EXISTS otp_challenges_email_idx ON otp_challenges (email, created_at DESC);
