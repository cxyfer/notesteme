-- Batch API (BATCH). See migration 00024 for the columns.

-- name: InsertBatch :exec
INSERT INTO batches (id, provider, model, external_id, state, submitted_at, deadline_at, task_ids_json)
VALUES (sqlc.arg(id), sqlc.arg(provider), sqlc.arg(model), '', 'submitted', sqlc.arg(submitted_at), sqlc.arg(deadline_at),
        sqlc.arg(task_ids_json));

-- name: BatchByID :one
SELECT * FROM batches WHERE id = sqlc.arg(id);

-- name: SetBatchExternalID :execrows
UPDATE batches SET external_id = sqlc.arg(external_id) WHERE id = sqlc.arg(id) AND external_id = '';

-- name: SetBatchState :exec
-- ended_at and notice are only filled in once.
UPDATE batches
SET state = sqlc.arg(state), ended_at = COALESCE(ended_at, sqlc.narg(ended_at)),
    notice = CASE WHEN notice = '' THEN sqlc.arg(notice) ELSE notice END
WHERE id = sqlc.arg(id);

-- name: ParkLeasedTask :execrows
-- The task that submits the batch gives up its lease and waits for the result (BATCH-R3).
UPDATE tasks SET state = 'waiting', batch_id = sqlc.arg(batch_id), lease_token = NULL, lease_owner = NULL, lease_until = NULL
WHERE id = sqlc.arg(id) AND state = 'leased' AND lease_token = CAST(sqlc.arg(lease_token) AS TEXT);

-- name: ParkPendingTask :execrows
-- Another claimable task of the same round joins the batch (BATCH-R2). It was never claimed, so attempts is unchanged.
UPDATE tasks SET state = 'waiting', batch_id = sqlc.arg(batch_id)
WHERE id = sqlc.arg(id) AND state = 'pending';

-- name: UnparkTask :execrows
-- A waiting task goes back to the regular API (BATCH-R4, R5): pending, due now, out of the batch, with a new payload
-- that tells the chunk task why.
UPDATE tasks SET state = 'pending', batch_id = NULL, payload_json = sqlc.arg(payload_json), not_before = NULL
WHERE id = sqlc.arg(id) AND state = 'waiting' AND batch_id = sqlc.arg(batch_id);

-- name: FinishBatchTask :execrows
-- The batch result completed the chunk.
UPDATE tasks SET state = 'done'
WHERE id = sqlc.arg(id) AND state = 'waiting' AND batch_id = sqlc.arg(batch_id);

-- name: BatchCandidateTasks :many
-- Claimable chunk tasks that may join a batch: pending, due, built-in, llm class, background priority
-- (subscription or backfill, BATCH-R1). The caller checks the rest (profile, provider, model). Claim order.
SELECT t.* FROM tasks AS t
WHERE t.state = 'pending' AND t.executor = 'builtin' AND t.resource_class = 'llm' AND t.chunk_index IS NOT NULL
  AND t.priority <= CAST(sqlc.arg(max_priority) AS INTEGER) AND t.id <> sqlc.arg(exclude_id)
  AND (t.not_before IS NULL OR t.not_before <= CAST(sqlc.arg(now) AS INTEGER))
ORDER BY t.priority DESC, t.created_at, t.stage_run_id, t.rowid
LIMIT sqlc.arg(max);

-- name: WaitingBatchOfRun :one
-- The batch a stage run is waiting for (GET /api/items/:id).
SELECT b.* FROM batches AS b
WHERE b.id = (SELECT t.batch_id FROM tasks AS t
              WHERE t.stage_run_id = sqlc.arg(stage_run_id) AND t.state = 'waiting' AND t.batch_id IS NOT NULL
              ORDER BY t.rowid LIMIT 1);

-- name: KickBatchPollsOfRun :exec
-- A stage run stops waiting (rerun, cancel): its batches are polled in the next round (BATCH-R9).
UPDATE scheduled_jobs SET next_run_at = CAST(sqlc.arg(now) AS INTEGER)
WHERE kind = 'batch.poll' AND next_run_at > CAST(sqlc.arg(now) AS INTEGER)
  AND key IN (SELECT t.batch_id FROM tasks AS t
              WHERE t.stage_run_id = sqlc.arg(stage_run_id) AND t.state = 'waiting' AND t.batch_id IS NOT NULL);

-- name: KickBatchPollsOfItem :exec
UPDATE scheduled_jobs SET next_run_at = CAST(sqlc.arg(now) AS INTEGER)
WHERE kind = 'batch.poll' AND next_run_at > CAST(sqlc.arg(now) AS INTEGER)
  AND key IN (SELECT t.batch_id FROM tasks AS t JOIN stage_runs AS sr ON sr.id = t.stage_run_id
              WHERE sr.item_id = sqlc.arg(item_id) AND t.state = 'waiting' AND t.batch_id IS NOT NULL);

-- name: SetLLMChunkBatch :exec
UPDATE llm_chunks SET batch_json = sqlc.arg(batch_json)
WHERE stage_run_id = sqlc.arg(stage_run_id) AND chunk = sqlc.arg(chunk);
