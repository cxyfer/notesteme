-- +goose Up
-- Trash and permanent delete (DEL-R4 to R7, R11). Times are unix milliseconds from the injected clock.

-- A tombstone remembers a permanently deleted item so that the inbox and subscriptions do not add it back.
-- part is 0 for platforms without parts, sha256 is empty for platform items (it is only set for local files).
CREATE TABLE item_tombstones (
    platform    TEXT NOT NULL,
    external_id TEXT NOT NULL,
    part        INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',
    purged_at   INTEGER NOT NULL,
    PRIMARY KEY (platform, external_id, part, sha256)
) STRICT;

CREATE INDEX item_tombstones_sha ON item_tombstones (sha256) WHERE sha256 <> '';

-- The cost ledger outlives its item: item_id becomes NULL when the item is purged and item_title keeps the title.
-- stage_run_id and task_id have no foreign key on purpose: the rows they point to are deleted with the item.
-- state: reserved | actual | unknown.
CREATE TABLE cost_ledger (
    id              TEXT PRIMARY KEY,
    item_id         TEXT REFERENCES items (id) ON DELETE SET NULL,
    item_title      TEXT NOT NULL DEFAULT '',
    stage_run_id    TEXT,
    task_id         TEXT,
    provider        TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    units           REAL NOT NULL DEFAULT 0,
    unit_type       TEXT NOT NULL DEFAULT '',
    usd             REAL NOT NULL,
    state           TEXT NOT NULL,
    attempt         INTEGER NOT NULL DEFAULT 0,
    outcome         TEXT NOT NULL DEFAULT '',
    unit_price_json TEXT NOT NULL DEFAULT '{}',
    at              INTEGER NOT NULL
) STRICT;

CREATE INDEX cost_ledger_item ON cost_ledger (item_id);

-- The trash view and the daily cleanup only read items that are in the trash.
CREATE INDEX items_deleted ON items (deleted_at) WHERE deleted_at IS NOT NULL;

-- Purging an item deletes its events by item_id.
CREATE INDEX events_item ON events (item_id) WHERE item_id <> '';

-- +goose Down
DROP INDEX events_item;
DROP INDEX items_deleted;
DROP INDEX cost_ledger_item;
DROP TABLE cost_ledger;
DROP INDEX item_tombstones_sha;
DROP TABLE item_tombstones;
