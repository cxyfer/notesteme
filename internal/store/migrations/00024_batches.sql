-- +goose Up
-- Batch API (BATCH, ADR-0021 sections 6 and 7). Times are unix milliseconds.
--
-- batches: one provider batch. external_id is the provider's batch id ('' until the submit returns; a row that
-- stays '' was left by a crash between the two transactions and is treated as a failed submit). The API key is
-- never stored (BATCH-R11). task_ids_json lists the requests: [{"task", "run", "chunk", "reservation"}].
-- state: submitted | cancelling | ended | cancelled | failed. notice is the reminder shown on the items when the
-- batch fell back for a reason the owner should know (for example a results URL on another host).
CREATE TABLE batches (
    id            TEXT    PRIMARY KEY,
    provider      TEXT    NOT NULL,
    model         TEXT    NOT NULL,
    external_id   TEXT    NOT NULL DEFAULT '',
    state         TEXT    NOT NULL,
    submitted_at  INTEGER NOT NULL,
    deadline_at   INTEGER NOT NULL,
    ended_at      INTEGER,
    task_ids_json TEXT    NOT NULL DEFAULT '[]',
    notice        TEXT    NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX batches_open ON batches (state) WHERE state IN ('submitted', 'cancelling');

-- A task waiting for a batch result has state 'waiting' and batch_id set; it is neither claimable nor part of
-- the lease recovery (BATCH-R3, R10). batch_id stays set when the task is removed (rerun, cancel, trash) so the
-- reservation is kept until the result is recorded as discarded (BATCH-R9).
ALTER TABLE tasks ADD COLUMN batch_id TEXT;
CREATE INDEX tasks_batch ON tasks (batch_id) WHERE batch_id IS NOT NULL;

-- batch_json is the per-chunk batch record for the provenance (BATCH-R8): mode, batch_id, submitted_at,
-- ended_at, fallback_reason. '{}' for chunks that never took part in a batch.
ALTER TABLE llm_chunks ADD COLUMN batch_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE llm_chunks DROP COLUMN batch_json;
DROP INDEX tasks_batch;
ALTER TABLE tasks DROP COLUMN batch_id;
DROP INDEX batches_open;
DROP TABLE batches;
