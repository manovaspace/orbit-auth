-- +goose Up
-- Self-serve sign-up keeps an account unusable until the email address is
-- verified by OTP. Every account that existed before this migration was
-- provisioned by a trusted path (email OTP, admin or demo seed), so its email
-- is already proven. Backfilling created_at keeps those users able to log in
-- and to accept organization invitations.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_verified_at TIMESTAMPTZ;

UPDATE users
SET email_verified_at = created_at
WHERE email IS NOT NULL
  AND email_verified_at IS NULL;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
