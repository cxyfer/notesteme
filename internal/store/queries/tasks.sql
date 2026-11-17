-- name: InsertTask :exec
INSERT INTO tasks (id, stage_run_id, kind, executor, resource_class, state, priority, payload_json,
                   max_attempts, not_before, deadline_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(stage_run_id), sqlc.arg(kind), sqlc.arg(executor), sqlc.arg(resource_class), 'pending',
        sqlc.arg(priority), sqlc.arg(payload_json), sqlc.arg(max_attempts), sqlc.narg(not_before), sqlc.narg(deadline_at),
        sqlc.arg(created_at));

-- name: TaskByID :one
SELECT * FROM tasks WHERE id = sqlc.arg(id);

-- name: TaskByStageRun :one
SELECT * FROM tasks WHERE stage_run_id = sqlc.arg(stage_run_id) ORDER BY created_at DESC, rowid DESC LIMIT 1;

-- name: ClaimTasks :many
-- Atomically claim runnable tasks in one statement (ADR-0004).
-- Claimable: state is pending, executor and resource class match, not_before has passed.
-- Order: priority plus aging (one level per aging_ms of waiting, PIPE-R9), then creation order.
-- resource_classes is a JSON array of strings; an empty array means every class. (sqlc.slice is not
-- used: its sqlite output mixes numbered and plain placeholders and breaks with more than one value.)
-- The score is computed in the select list because sqlc does not rewrite parameters inside ORDER BY.
-- RETURNING has no defined order, so the caller must re-sort the result by the same key.
UPDATE tasks
SET state = 'leased', lease_owner = CAST(sqlc.arg(owner) AS TEXT), lease_token = CAST(sqlc.arg(token) AS TEXT),
    lease_until = CAST(sqlc.arg(lease_until) AS INTEGER), attempts = attempts + 1
WHERE id IN (
  SELECT picked.id FROM (
    SELECT t.id AS id, t.priority + (CAST(sqlc.arg(now) AS INTEGER) - t.created_at) / CAST(sqlc.arg(aging_ms) AS INTEGER) AS score
    FROM tasks AS t
    WHERE t.state = 'pending' AND (t.not_before IS NULL OR t.not_before <= CAST(sqlc.arg(now) AS INTEGER))
      AND t.executor = sqlc.arg(executor)
      AND (json_array_length(CAST(sqlc.arg(resource_classes) AS TEXT)) = 0
           OR t.resource_class IN (SELECT j.value FROM json_each(CAST(sqlc.arg(resource_classes) AS TEXT)) AS j))
    ORDER BY score DESC, t.created_at, t.rowid
    LIMIT sqlc.arg(max)
  ) AS picked
)
RETURNING *;

-- name: SetTaskLeaseToken :exec
UPDATE tasks SET lease_token = CAST(sqlc.arg(lease_token) AS TEXT) WHERE id = sqlc.arg(id);

-- name: CompleteTask :execrows
-- Only the holder of the current lease can write the result back: 0 rows means the lease was lost.
UPDATE tasks SET state = 'done', lease_token = NULL, lease_owner = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND state = 'leased' AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT);

-- name: FailTask :execrows
UPDATE tasks SET state = 'failed', lease_token = NULL, lease_owner = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND state = 'leased' AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT);

-- name: RequeueTask :execrows
-- count_attempt = 0 means this claim does not count towards attempts.
UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
  not_before = sqlc.narg(not_before),
  attempts = CASE WHEN CAST(sqlc.arg(count_attempt) AS INTEGER) <> 0 THEN attempts ELSE MAX(attempts - 1, 0) END
WHERE id = sqlc.arg(id) AND state = 'leased' AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT);

-- name: ReleaseLeases :many
-- Leased tasks not held by keep_owner, or already expired, go back to pending without counting an attempt.
UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
  attempts = MAX(attempts - 1, 0)
WHERE state = 'leased' AND (lease_owner <> CAST(sqlc.arg(keep_owner) AS TEXT) OR lease_until <= CAST(sqlc.arg(now) AS INTEGER))
RETURNING stage_run_id;

-- name: ReleaseOwnedLeases :many
UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
  attempts = MAX(attempts - 1, 0)
WHERE state = 'leased' AND lease_owner = CAST(sqlc.arg(owner) AS TEXT)
RETURNING stage_run_id;

-- name: NextTaskTime :one
-- Earliest not_before strictly after after_ms among pending tasks; no row when no pending task waits for a later time.
-- Tasks that are already due are left out on purpose: when they cannot be claimed (paused or full class),
-- they must not hide a later retry time. A wake-up covers the change that makes them claimable.
SELECT CAST(COALESCE(not_before, 0) AS INTEGER) AS due_ms FROM tasks
WHERE state = 'pending' AND executor = sqlc.arg(executor)
  AND not_before IS NOT NULL AND not_before > CAST(sqlc.arg(after_ms) AS INTEGER)
ORDER BY not_before LIMIT 1;

-- name: CountTasks :one
SELECT COUNT(*) FROM tasks WHERE state = sqlc.arg(state);

-- name: CancelPendingTasks :execrows
UPDATE tasks SET state = 'failed' WHERE stage_run_id = sqlc.arg(stage_run_id) AND state = 'pending';

-- name: CancelItemTasks :many
-- Cancel every unfinished (pending or leased) task of an item and drop its lease, so a late write-back
-- from the former holder matches no row. Returns the ids so the worker can stop what is running.
UPDATE tasks SET state = 'cancelled', lease_token = NULL, lease_owner = NULL, lease_until = NULL
WHERE state IN ('pending', 'leased')
  AND stage_run_id IN (SELECT id FROM stage_runs WHERE item_id = sqlc.arg(item_id))
RETURNING id;

-- name: ListQueueTasks :many
-- Unfinished tasks of one executor for the queue page: leased first, then pending in claim order.
-- Pending tasks that are not due yet (not_before in the future) come after the due ones, because
-- ClaimTasks skips them. Due pending tasks use the same score as ClaimTasks, so the first pending
-- row is the task the next claim takes. The score is computed in the select list because sqlc does
-- not rewrite parameters inside ORDER BY.
SELECT t.id AS id, t.kind AS kind, t.resource_class AS resource_class, t.state AS state, t.attempts AS attempts,
       t.priority AS priority, t.not_before AS not_before, sr.item_id AS item_id,
       i.platform AS platform, i.external_id AS external_id, i.part AS part,
       CASE WHEN t.state = 'leased' THEN 0
            WHEN t.not_before IS NOT NULL AND t.not_before > CAST(sqlc.arg(now) AS INTEGER) THEN 2
            ELSE 1 END AS bucket,
       t.priority + (CAST(sqlc.arg(now) AS INTEGER) - t.created_at) / CAST(sqlc.arg(aging_ms) AS INTEGER) AS score
FROM tasks AS t
JOIN stage_runs AS sr ON sr.id = t.stage_run_id
JOIN items AS i ON i.id = sr.item_id
WHERE t.state IN ('leased', 'pending') AND t.executor = sqlc.arg(executor) AND i.deleted_at IS NULL
ORDER BY bucket, score DESC, t.created_at, t.rowid;
