-- +goose Up
-- LLM chunks (LLM-R1, LLM-R8, ADR-0004): one row per chunk of one LLM stage run.
-- The coordinator task of the stage run inserts every row; a chunk task fills its row in.
-- status: pending | done | fallback. Only done rows are reused by a later run with the same
-- cache_key (the chunk input, the fixed prompt prefix, provider, model and implementation).
-- plan_json holds the chunk range; output_json holds the validated output (NULL for pending
-- and fallback rows); reason is the fallback reason; usage_json is the token usage.
CREATE TABLE llm_chunks (
    stage_run_id TEXT NOT NULL REFERENCES stage_runs (id) ON DELETE CASCADE,
    chunk        INTEGER NOT NULL,
    item_id      TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    stage        TEXT NOT NULL,
    cache_key    TEXT NOT NULL,
    status       TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    plan_json    TEXT NOT NULL,
    output_json  TEXT,
    usage_json   TEXT NOT NULL DEFAULT '{}',
    reused       INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (stage_run_id, chunk)
) STRICT;

CREATE INDEX llm_chunks_cache ON llm_chunks (item_id, stage, cache_key);

-- +goose Down
DROP TABLE llm_chunks;
