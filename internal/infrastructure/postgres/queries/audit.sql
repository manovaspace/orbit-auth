-- name: InsertAuditEvent :exec
INSERT INTO audit_events (occurred_at, action, actor_kind, actor_id, org_id, subject_user_id, invite_id, reason, details)
VALUES (
    sqlc.arg('occurred_at')::timestamptz,
    sqlc.arg('action')::text,
    sqlc.arg('actor_kind')::text,
    sqlc.arg('actor_id')::text,
    sqlc.narg('org_id')::uuid,
    sqlc.narg('subject_user_id')::uuid,
    sqlc.narg('invite_id')::uuid,
    sqlc.arg('reason')::text,
    sqlc.arg('details')::jsonb
);

-- name: ListAuditEvents :many
-- One page of audit rows, newest first. A narg filter is ignored when NULL. The
-- keyset (id) resumes before the previous page's last row.
SELECT id,
       occurred_at,
       action,
       actor_kind,
       actor_id,
       COALESCE(org_id::text, '')::text AS org_id,
       COALESCE(subject_user_id::text, '')::text AS subject_user_id,
       COALESCE(invite_id::text, '')::text AS invite_id,
       reason,
       details::text AS details
FROM audit_events
WHERE (sqlc.narg('org_id')::uuid IS NULL OR org_id = sqlc.narg('org_id')::uuid)
  AND (sqlc.narg('subject_user_id')::uuid IS NULL OR subject_user_id = sqlc.narg('subject_user_id')::uuid)
  AND (sqlc.narg('after_id')::bigint IS NULL OR id < sqlc.narg('after_id')::bigint)
ORDER BY id DESC
LIMIT sqlc.arg('row_limit')::int;
