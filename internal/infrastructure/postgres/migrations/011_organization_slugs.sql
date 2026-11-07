-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = '60s';

-- Every organization gets a slug: the label of its tenant host, for example
-- <slug>.falak.manova.space. Slugs are 3 to 30 lowercase letters, digits and
-- single hyphens, start with a letter and end with a letter or digit. The
-- reserved list and uniqueness are enforced by orbit-auth; the check constraint
-- here only pins the format.
ALTER TABLE organizations ADD COLUMN IF NOT EXISTS slug TEXT;

-- Backfill: the name when it is already a valid, unreserved slug and nobody
-- older holds it, otherwise "org-" and the first twelve hex digits of the id.
-- Organizations created before this migration can be renamed afterwards.
WITH candidates AS (
    SELECT id,
           created_at,
           trim(BOTH '-' FROM regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g')) AS candidate
    FROM organizations
), ranked AS (
    SELECT id,
           candidate,
           row_number() OVER (PARTITION BY candidate ORDER BY created_at, id) AS position
    FROM candidates
    WHERE candidate ~ '^[a-z][a-z0-9-]{1,28}[a-z0-9]$'
      AND candidate !~ '--'
      AND candidate NOT IN (
        'admin', 'administrator', 'api', 'app', 'assets', 'auth', 'billing', 'cdn',
        'dev', 'demo', 'docs', 'falak', 'git', 'handbook', 'help', 'internal',
        'login', 'logout', 'mail', 'manova', 'orbit', 'root', 'signup', 'smtp',
        'sta', 'staging', 'static', 'status', 'support', 'system', 'test', 'www')
)
UPDATE organizations o
SET slug = ranked.candidate
FROM ranked
WHERE o.id = ranked.id
  AND ranked.position = 1;

UPDATE organizations
SET slug = 'org-' || left(replace(id::text, '-', ''), 12)
WHERE slug IS NULL;

-- squawk-ignore adding-not-nullable-field, prefer-robust-stmts -- the table holds one row per organization and goose runs this file in one transaction
ALTER TABLE organizations ALTER COLUMN slug SET NOT NULL;
-- squawk-ignore constraint-missing-not-valid, prefer-robust-stmts -- the table is tiny and every existing row was just backfilled to match
ALTER TABLE organizations ADD CONSTRAINT organizations_slug_format CHECK (slug ~ '^[a-z][a-z0-9-]{1,28}[a-z0-9]$' AND slug !~ '--');
-- squawk-ignore require-concurrent-index-creation -- a concurrent build cannot run inside goose's transaction; the table is tiny
CREATE UNIQUE INDEX IF NOT EXISTS organizations_slug_key ON organizations (slug);

-- Registry of every slug an organization has held. The primary key makes a
-- slug unique across all organizations and across time, so a retired slug
-- cannot be claimed by another organization (no host takeover) and the old
-- host can redirect to the new one. The current slug has retired_at NULL.
CREATE TABLE IF NOT EXISTS organization_slugs (
    slug TEXT PRIMARY KEY
        CHECK (slug ~ '^[a-z][a-z0-9-]{1,28}[a-z0-9]$' AND slug !~ '--'),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at TIMESTAMPTZ
);

-- squawk-ignore require-concurrent-index-creation -- the table was just created in this transaction
CREATE UNIQUE INDEX IF NOT EXISTS organization_slugs_current_idx
    ON organization_slugs (org_id)
    WHERE retired_at IS NULL;

INSERT INTO organization_slugs (slug, org_id, created_at)
SELECT slug, id, created_at FROM organizations
ON CONFLICT (slug) DO NOTHING;

-- +goose Down
DROP TABLE organization_slugs;
DROP INDEX organizations_slug_key;
ALTER TABLE organizations DROP CONSTRAINT organizations_slug_format;
ALTER TABLE organizations DROP COLUMN slug;
