-- +goose Up
-- Persisted SSE events (OPS-R10). Times are unix milliseconds from the injected clock.

-- id is a UUIDv7, item_id is empty for events that do not belong to an item (no foreign key on purpose:
-- events outlive a purged item until the retention cleanup removes them).
CREATE TABLE events (
    id           TEXT PRIMARY KEY,
    item_id      TEXT NOT NULL DEFAULT '',
    type         TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    at           INTEGER NOT NULL
) STRICT;

CREATE INDEX events_at ON events (at);

-- +goose Down
DROP INDEX events_at;
DROP TABLE events;
