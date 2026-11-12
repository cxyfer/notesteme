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

-- name: CompleteTask :exec
UPDATE tasks SET state = 'done', lease_token = NULL, lease_owner = NULL, lease_until = NULL WHERE id = sqlc.arg(id);

-- name: FailTask :exec
UPDATE tasks SET state = 'failed', lease_token = NULL, lease_owner = NULL, lease_until = NULL WHERE id = sqlc.arg(id);

-- name: RequeueTask :exec
-- count_attempt = 0 means this claim does not count towards attempts.
UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
  not_before = sqlc.narg(not_before),
  attempts = CASE WHEN CAST(sqlc.arg(count_attempt) AS INTEGER) <> 0 THEN attempts ELSE MAX(attempts - 1, 0) END
WHERE id = sqlc.arg(id);

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
-- Earliest time a pending task can be claimed (NULL not_before counts as 0); no row when nothing is pending.
SELECT CAST(COALESCE(not_before, 0) AS INTEGER) AS due_ms FROM tasks
WHERE state = 'pending' AND executor = sqlc.arg(executor)
ORDER BY COALESCE(not_before, 0) LIMIT 1;

-- name: CountTasks :one
SELECT COUNT(*) FROM tasks WHERE state = sqlc.arg(state);

-- name: CancelPendingTasks :execrows
UPDATE tasks SET state = 'failed' WHERE stage_run_id = sqlc.arg(stage_run_id) AND state = 'pending';
