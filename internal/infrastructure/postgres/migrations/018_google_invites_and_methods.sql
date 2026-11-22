-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- A Google sign-in state now says what it is for. A sign_in state carries no
-- invitation and no user. An invite state carries the invitation it accepts
-- (never its token). A link state carries the account that links Google. The
-- check pins those shapes; the existing rows are all sign_in rows, so the
-- constraint holds for them.
ALTER TABLE oauth_sign_in_states ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'sign_in' CHECK (purpose IN ('sign_in', 'invite', 'link'));
-- The table holds states for ten minutes, so the scan of each new constraint is
-- short, and lock_timeout bounds the wait for the referenced tables.
-- squawk-ignore adding-foreign-key-constraint -- the column is new and empty, and the table holds only short-lived rows
ALTER TABLE oauth_sign_in_states ADD COLUMN IF NOT EXISTS invite_id UUID NULL REFERENCES organization_invites(id) ON DELETE CASCADE;
-- squawk-ignore adding-foreign-key-constraint -- the column is new and empty, and the table holds only short-lived rows
ALTER TABLE oauth_sign_in_states ADD COLUMN IF NOT EXISTS user_id UUID NULL REFERENCES users(id) ON DELETE CASCADE;
-- squawk-ignore constraint-missing-not-valid, prefer-robust-stmts -- the table holds only short-lived rows, and goose wraps this migration in a transaction
ALTER TABLE oauth_sign_in_states ADD CONSTRAINT oauth_sign_in_states_purpose_chk CHECK ((purpose = 'sign_in' AND invite_id IS NULL AND user_id IS NULL) OR (purpose = 'invite' AND invite_id IS NOT NULL AND user_id IS NULL) OR (purpose = 'link' AND user_id IS NOT NULL AND invite_id IS NULL));

-- +goose Down
-- squawk-ignore ban-drop-table -- rollback removes the states of the new purposes; a state is valid for ten minutes and no code reads the columns after down
DELETE FROM oauth_sign_in_states WHERE purpose <> 'sign_in';
-- squawk-ignore ban-drop-constraint -- rollback of the purpose check added by this migration; no code reads it after down
ALTER TABLE oauth_sign_in_states DROP CONSTRAINT IF EXISTS oauth_sign_in_states_purpose_chk;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE oauth_sign_in_states DROP COLUMN IF EXISTS user_id;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE oauth_sign_in_states DROP COLUMN IF EXISTS invite_id;
-- squawk-ignore ban-drop-column -- rollback of the column added by this migration; no code reads it after down
ALTER TABLE oauth_sign_in_states DROP COLUMN IF EXISTS purpose;
