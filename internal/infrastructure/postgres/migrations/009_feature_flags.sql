-- +goose Up
-- Platform feature flags are public configuration: the frontend reads them
-- before sign-in, so names and states are not secrets.
CREATE TABLE IF NOT EXISTS feature_flags (
    name TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    description TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO feature_flags (name, enabled, description)
VALUES ('manova.frontend.locale_switcher', TRUE, 'Locale switcher in the Orbit frontend template')
ON CONFLICT (name) DO NOTHING;

-- +goose Down
DROP TABLE feature_flags;
