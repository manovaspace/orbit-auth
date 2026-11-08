-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- The time a mobile number was proven by a successful mobile OTP. NULL means
-- the number was never proven. Accounts created by accepting a phone invitation
-- with a password (AcceptInviteWithSignup) stay NULL until an OTP login for the
-- same number succeeds. Sign-in is not gated on this column.
ALTER TABLE users ADD COLUMN IF NOT EXISTS mobile_verified_at TIMESTAMPTZ;

-- Backfill: every account that already has a mobile number came from a mobile
-- OTP login or from demo seeding, so its number counts as verified.
UPDATE users
SET mobile_verified_at = NOW()
WHERE mobile IS NOT NULL
  AND mobile_verified_at IS NULL;

-- +goose Down
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE users DROP COLUMN IF EXISTS mobile_verified_at;
