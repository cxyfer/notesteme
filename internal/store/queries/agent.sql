-- name: CountAgentHeld :one
-- Leased agent tasks of one resource class that one agent holds (AGT-R2: its claims never exceed the class limit).
SELECT COUNT(*) FROM tasks
WHERE executor = 'agent' AND state = 'leased' AND lease_owner = CAST(sqlc.arg(owner) AS TEXT)
  AND resource_class = sqlc.arg(resource_class);

-- name: AgentTaskForLease :one
-- The agent task with this id, whatever its state; the caller checks the lease token, holder and expiry.
SELECT * FROM tasks WHERE id = sqlc.arg(id) AND executor = 'agent';

-- name: ExtendAgentLease :execrows
-- Only the holder of a lease that has not expired yet can extend it (AGT-R4).
UPDATE tasks SET lease_until = CAST(sqlc.arg(lease_until) AS INTEGER)
WHERE id = sqlc.arg(id) AND executor = 'agent' AND state = 'leased'
  AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT) AND lease_owner = CAST(sqlc.arg(owner) AS TEXT)
  AND lease_until > CAST(sqlc.arg(now) AS INTEGER);

-- name: AddAgentFailure :one
-- One more failed submission under the current lease (AGT-R6); returns the new count.
UPDATE tasks SET agent_failures = agent_failures + 1
WHERE id = sqlc.arg(id) AND executor = 'agent' AND state = 'leased'
  AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT)
RETURNING agent_failures;

-- name: RequeueAgentTask :execrows
-- Put a leased agent task back in the queue. count_attempt = 0 means this claim does not count towards
-- attempts (release_task, AGT-R8); an expired lease or repeated validation failures count (AGT-R4, R6).
-- deadline_at restarts the wait for a claim (AGT-R9 counts the time the task sits unclaimed).
UPDATE tasks SET state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL, agent_failures = 0,
  release_reason = sqlc.arg(release_reason), deadline_at = sqlc.narg(deadline_at),
  attempts = CASE WHEN CAST(sqlc.arg(count_attempt) AS INTEGER) <> 0 THEN attempts ELSE MAX(attempts - 1, 0) END
WHERE id = sqlc.arg(id) AND executor = 'agent' AND state = 'leased'
  AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT);

-- name: ExpiredAgentLeases :many
-- Leased agent tasks whose lease has run out (AGT-R4).
SELECT * FROM tasks
WHERE executor = 'agent' AND state = 'leased' AND lease_until <= CAST(sqlc.arg(now) AS INTEGER)
ORDER BY lease_until, rowid;

-- name: TimedOutAgentTasks :many
-- Pending agent tasks that nobody claimed before their deadline (AGT-R9).
SELECT * FROM tasks
WHERE executor = 'agent' AND state = 'pending' AND deadline_at IS NOT NULL
  AND deadline_at <= CAST(sqlc.arg(now) AS INTEGER)
ORDER BY deadline_at, rowid;

-- name: NextAgentTime :one
-- The earliest time the sweeper has something to do: a lease running out or a claim deadline passing.
SELECT CAST(COALESCE(MIN(t), 0) AS INTEGER) AS due_ms FROM (
  SELECT lease_until AS t FROM tasks WHERE executor = 'agent' AND state = 'leased' AND lease_until IS NOT NULL
  UNION ALL
  SELECT deadline_at AS t FROM tasks WHERE executor = 'agent' AND state = 'pending' AND deadline_at IS NOT NULL
);

-- name: ClearAgentDeadlines :exec
-- The owner was told about the timeout (on_timeout: notify): do not notify again for the same wait.
UPDATE tasks SET deadline_at = NULL
WHERE stage_run_id = sqlc.arg(stage_run_id) AND executor = 'agent' AND state = 'pending';

-- name: SwitchAgentTasksToBuiltin :many
-- Hand the unfinished agent tasks of a stage run to the API executor (AGT-R9, R11): the built-in worker
-- claims them next. A lease an agent holds ends at once, so its token no longer matches (AGT-R7).
UPDATE tasks SET executor = 'builtin', state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
  not_before = NULL, deadline_at = NULL, agent_failures = 0, attempts = 0
WHERE stage_run_id = sqlc.arg(stage_run_id) AND executor = 'agent' AND state IN ('pending', 'leased')
RETURNING id;

-- name: CountRunTasks :one
-- Tasks of a stage run in one state for one executor.
SELECT COUNT(*) FROM tasks
WHERE stage_run_id = sqlc.arg(stage_run_id) AND executor = sqlc.arg(executor) AND state = sqlc.arg(state);

-- name: AwaitAgentStage :one
-- A stage whose agent tasks are all back in the queue waits for an agent again (AGT-R1). Only a stage
-- that is running or already waiting changes; a stage that stopped or finished keeps its status.
UPDATE stage_runs SET status = 'awaiting_agent', block_reason = '', resume_on = '', error_class = '',
  error_message = '', actions_json = '[]', not_before = NULL
WHERE id = sqlc.arg(id) AND status IN ('running', 'pending')
RETURNING item_id, stage, status;

-- name: SwitchAgentTaskToBuiltin :many
-- The same as SwitchAgentTasksToBuiltin for one task (switch-executor of a task, on_validation_failure: fallback_api).
UPDATE tasks SET executor = 'builtin', state = 'pending', lease_token = NULL, lease_owner = NULL, lease_until = NULL,
  not_before = NULL, deadline_at = NULL, agent_failures = 0, attempts = 0
WHERE id = sqlc.arg(id) AND executor = 'agent' AND state IN ('pending', 'leased')
RETURNING id;
