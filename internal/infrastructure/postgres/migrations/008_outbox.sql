-- +goose Up
CREATE TABLE outbox_events (
    id bigserial PRIMARY KEY,
    subject text NOT NULL,
    envelope jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts int NOT NULL DEFAULT 0,
    last_error text
);

CREATE INDEX outbox_events_pending_idx ON outbox_events (id) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE outbox_events;
