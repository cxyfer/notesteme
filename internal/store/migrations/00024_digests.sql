-- +goose Up
-- Digests (DIG-R8, ADR-0021 section 7; DIG D10 lists the differences from the ADR). Times are unix milliseconds.
-- The next run lives only in scheduled_jobs (kind digest, key = digests.id). last_run_at is the scheduled end of
-- the range covered by the last run (DIG-R9), not the wall-clock time it ran; NULL until the first run.
CREATE TABLE digests (
    id                TEXT    NOT NULL PRIMARY KEY,
    period            TEXT    NOT NULL,
    at                TEXT    NOT NULL,
    weekday           TEXT    NOT NULL DEFAULT '',
    timezone          TEXT    NOT NULL,
    notifier_ids_json TEXT    NOT NULL,
    scope             TEXT    NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        INTEGER NOT NULL,
    last_run_at       INTEGER
) STRICT;

-- One row per item listed by a digest run (DIG-R9): an item appears in a given digest only once.
CREATE TABLE digest_entries (
    digest_id TEXT    NOT NULL REFERENCES digests (id) ON DELETE CASCADE,
    item_id   TEXT    NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    run_at    INTEGER NOT NULL,
    PRIMARY KEY (digest_id, item_id)
) STRICT;

CREATE INDEX digest_entries_item ON digest_entries (item_id);
-- Digest candidates are summary stage runs that finished inside the range.
CREATE INDEX stage_runs_stage_finished ON stage_runs (stage, finished_at);

-- +goose Down
DROP INDEX stage_runs_stage_finished;
DROP TABLE digest_entries;
DROP TABLE digests;
