-- +goose Up
-- Scheduled jobs (ADR-0021 section 1): one row per (kind, key), e.g. subscription.check with the subscription id
-- as key. Times are unix milliseconds. next_run_at is when the job is due; failures counts consecutive failures for
-- the backoff; last_ok_at is NULL until the first success (OPS-R15 then falls back to created_at). The implicit
-- rowid is kept on purpose: jobs due at the same time run in creation order (SUB-R9), and the fake clock in tests
-- often gives several rows the same created_at.
CREATE TABLE scheduled_jobs (
    kind        TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    next_run_at INTEGER NOT NULL,
    last_run_at INTEGER,
    last_ok_at  INTEGER,
    last_error  TEXT,
    failures    INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    PRIMARY KEY (kind, key)
) STRICT;

CREATE INDEX scheduled_jobs_next_run_at ON scheduled_jobs (next_run_at);

-- +goose Down
DROP TABLE scheduled_jobs;
