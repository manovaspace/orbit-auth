-- +goose Up
CREATE TABLE IF NOT EXISTS organization_invites (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    inviter_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'accepted', 'revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- At most one pending invite per organization and email address. The same
-- address may have older accepted or revoked invites, and the same address may
-- be invited to several organizations.
CREATE UNIQUE INDEX IF NOT EXISTS organization_invites_pending_email_idx
    ON organization_invites (org_id, lower(email))
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS organization_invites_org_idx
    ON organization_invites (org_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS organization_invites;
