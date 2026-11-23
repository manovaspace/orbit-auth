-- Read-only pre-checks before migrations 019 to 024 run on a database that
-- already holds data (production `auth`). Run as the service role:
--   psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f scripts/prechecks-019-024.sql
-- Every row named "block:" must show 0 (or true where it says so); anything
-- else stops the rollout until the owner decides how to fix the data.
-- Rows named "info:" are for the record only. Nothing here writes.
SET default_transaction_read_only = on;
SET search_path = public;

-- Where the database is now. The goose history must exist (a database the old
-- runner left behind is adopted first; see Production adoption). 018 or later
-- is expected; 024 means nothing is left to run.
SELECT 'block (must be true): goose history present' AS check, (to_regclass('public.goose_db_version') IS NOT NULL)::text AS result;
SELECT 'info: goose version', max(version_id)::text FROM goose_db_version WHERE is_applied;

-- 019: case-insensitive unique email, E.164 mobile, OTP channel, expiry after creation.
SELECT 'block: duplicate emails ignoring case', count(*)::text FROM (
  SELECT lower(email) FROM users WHERE email IS NOT NULL GROUP BY 1 HAVING count(*) > 1) x;
SELECT 'block: mobiles not in E.164', count(*)::text FROM users
  WHERE mobile IS NOT NULL AND mobile !~ '^\+[1-9][0-9]{6,14}$';
SELECT 'block: OTP channel not email or sms', count(*)::text FROM otp_challenges WHERE channel NOT IN ('email', 'sms');
SELECT 'block: OTP expiry not after creation', count(*)::text FROM otp_challenges WHERE expires_at <= created_at;
SELECT 'block: refresh expiry not after creation', count(*)::text FROM refresh_tokens WHERE expires_at <= created_at;
SELECT 'block: invite expiry not after creation', count(*)::text FROM organization_invites WHERE expires_at <= created_at;
SELECT 'block: Google state expiry not after creation', count(*)::text FROM oauth_sign_in_states WHERE expires_at <= created_at;

-- 020: unique refresh token hash.
SELECT 'block: duplicate refresh token hashes', count(*)::text FROM (
  SELECT token_hash FROM refresh_tokens GROUP BY 1 HAVING count(*) > 1) x;

-- 021: refresh tokens point at real users and organizations.
SELECT 'block: refresh tokens without a user', count(*)::text FROM refresh_tokens r
  WHERE NOT EXISTS (SELECT 1 FROM users u WHERE u.id = r.user_id);
SELECT 'block: refresh tokens without their organization', count(*)::text FROM refresh_tokens r
  WHERE r.org_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM organizations o WHERE o.id = r.org_id);

-- 022 and 023 add columns with backfills; they need no data check. Sizes decide
-- whether the plain index builds in 022 and 023 are acceptable (expected: kB to a few MB).
SELECT 'info: size ' || relname, pg_size_pretty(pg_total_relation_size(oid)) FROM pg_class
  WHERE relname IN ('users', 'refresh_tokens', 'otp_challenges', 'organization_invites') AND relkind = 'r'
    AND relnamespace = 'public'::regnamespace;

-- 024: the schemas must not exist yet, and the service role must be able to create them.
SELECT 'block: target schemas already present', count(*)::text FROM pg_namespace
  WHERE nspname IN ('identity', 'tenancy', 'platform');
SELECT 'block (must be true): role may create schemas', has_database_privilege(current_user, current_database(), 'CREATE')::text;
SELECT 'block (must be true): role owns every auth table', bool_and(tableowner = current_user)::text FROM pg_tables
  WHERE schemaname = 'public' AND tablename <> 'goose_db_version';
