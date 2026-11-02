-- +goose Up
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS org_id UUID;

-- +goose Down
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS org_id;
