-- Scheduled jobs (ADR-0021 section 1). kinds is a JSON array of strings: only registered kinds are considered, so
-- rows of a kind this build does not know never block the others. (sqlc.slice is not used, see tasks.sql.)

-- name: UpsertScheduledJob :exec
-- Schedule a job at next_run_at; an existing job keeps its history and only moves its next run.
INSERT INTO scheduled_jobs (kind, key, next_run_at, created_at)
VALUES (sqlc.arg(kind), sqlc.arg(key), sqlc.arg(next_run_at), sqlc.arg(created_at))
ON CONFLICT (kind, key) DO UPDATE SET next_run_at = excluded.next_run_at;

-- name: InsertScheduledJobIfAbsent :execrows
INSERT INTO scheduled_jobs (kind, key, next_run_at, created_at)
VALUES (sqlc.arg(kind), sqlc.arg(key), sqlc.arg(next_run_at), sqlc.arg(created_at))
ON CONFLICT (kind, key) DO NOTHING;

-- name: GetScheduledJob :one
SELECT * FROM scheduled_jobs WHERE kind = sqlc.arg(kind) AND key = sqlc.arg(key);

-- name: DeleteScheduledJob :execrows
DELETE FROM scheduled_jobs WHERE kind = sqlc.arg(kind) AND key = sqlc.arg(key);

-- name: DueScheduledJobs :many
-- Due jobs of the given kinds, by next_run_at and then creation order.
SELECT * FROM scheduled_jobs
WHERE next_run_at <= sqlc.arg(now)
  AND kind IN (SELECT j.value FROM json_each(CAST(sqlc.arg(kinds) AS TEXT)) AS j)
ORDER BY next_run_at, rowid
LIMIT sqlc.arg(max);

-- name: NextScheduledJobAt :one
-- jobs is 0 when nothing of these kinds is scheduled (next_run_at is then 0, not NULL).
SELECT CAST(COUNT(*) AS INTEGER) AS jobs, CAST(COALESCE(MIN(next_run_at), 0) AS INTEGER) AS next_run_at FROM scheduled_jobs
WHERE kind IN (SELECT j.value FROM json_each(CAST(sqlc.arg(kinds) AS TEXT)) AS j);

-- name: RecordScheduledJobRun :execrows
-- Store the outcome of one run (the caller computes every field).
UPDATE scheduled_jobs
SET next_run_at = sqlc.arg(next_run_at), last_run_at = sqlc.arg(last_run_at), last_ok_at = sqlc.arg(last_ok_at),
    last_error = sqlc.arg(last_error), failures = sqlc.arg(failures)
WHERE kind = sqlc.arg(kind) AND key = sqlc.arg(key);
