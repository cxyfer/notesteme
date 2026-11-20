-- +goose Up
-- Local media (ING-05). source_path is an absolute path for inbox and media_roots files, and
-- uploads/<id>/<name> relative to the data directory for uploads. origin is manual | upload | inbox.
-- content_sha256 stays empty until the metadata stage hashes the file.

ALTER TABLE items ADD COLUMN source_path TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN content_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual';

-- Duplicate detection by content only looks at local items that have been hashed.
CREATE INDEX items_local_sha ON items (content_sha256) WHERE platform = 'local' AND content_sha256 <> '';

-- What the inbox watcher decided about each file, keyed by its path relative to the inbox.
-- state: ingested | already | duplicate | not_media | tombstoned | in_trash | conflict.
-- item_id becomes NULL when the item is purged; the entry (and its decision) stays.
CREATE TABLE inbox_entries (
    path       TEXT PRIMARY KEY,
    size       INTEGER NOT NULL,
    mtime_ns   INTEGER NOT NULL,
    sha256     TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    item_id    TEXT REFERENCES items (id) ON DELETE SET NULL,
    decided_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE inbox_entries;
DROP INDEX items_local_sha;
ALTER TABLE items DROP COLUMN origin;
ALTER TABLE items DROP COLUMN content_sha256;
ALTER TABLE items DROP COLUMN source_path;
