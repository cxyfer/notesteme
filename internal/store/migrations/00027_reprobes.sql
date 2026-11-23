-- +goose Up
-- SRC-10 (SRC-R18～R23): re-probing after processing. The next run lives only in scheduled_jobs
-- (kind source.reprobe, key = items.id); this table keeps what the job checks and the better source found.
-- probe_trigger tells stage probes (shown by GET /api/items/:id) from reprobe results (SRC-R19);
-- the spec calls the column "trigger", which is an SQL keyword.
ALTER TABLE source_probes ADD COLUMN probe_trigger TEXT NOT NULL DEFAULT 'stage';

CREATE TABLE source_reprobes (
    item_id      TEXT    NOT NULL PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    until_at     INTEGER NOT NULL,
    runs         INTEGER NOT NULL DEFAULT 0,
    sources      INTEGER NOT NULL DEFAULT 0,
    sponsorblock INTEGER NOT NULL DEFAULT 0,
    -- better_json: {source, lang, kind, found_at, estimate_usd}; NULL when there is no better source.
    better_json  TEXT,
    auto_reruns  INTEGER NOT NULL DEFAULT 0,
    updated_at   INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE source_reprobes;
ALTER TABLE source_probes DROP COLUMN probe_trigger;
