-- +goose Up
-- +goose NO TRANSACTION
-- A refresh token is found and consumed by the hash of its value
-- (FindValidRefreshToken, ConsumeRefreshToken), and every lookup must hit at
-- most one row. The unique index serves the lookup and enforces the rule.
-- CONCURRENTLY builds it without blocking writes to refresh_tokens, which sign-in
-- and refresh write, so this file runs outside a transaction and
-- holds no other statement. A failed build leaves an INVALID index behind; drop it
-- before rerunning, because IF NOT EXISTS would otherwise keep it.
-- squawk-ignore require-lock-timeout, require-statement-timeout -- this file holds exactly one statement by design, so no SET can precede it; CONCURRENTLY takes SHARE UPDATE EXCLUSIVE, which does not block reads or writes
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_token_hash_key ON refresh_tokens (token_hash);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS refresh_tokens_token_hash_key;
