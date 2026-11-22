-- name: InsertLLMChunk :exec
INSERT INTO llm_chunks (stage_run_id, chunk, item_id, stage, cache_key, status, reason, plan_json,
                        output_json, usage_json, detail_json, reused, created_at, updated_at)
VALUES (sqlc.arg(stage_run_id), sqlc.arg(chunk), sqlc.arg(item_id), sqlc.arg(stage), sqlc.arg(cache_key),
        sqlc.arg(status), sqlc.arg(reason), sqlc.arg(plan_json), sqlc.narg(output_json), sqlc.arg(usage_json),
        sqlc.arg(detail_json), sqlc.arg(reused), sqlc.arg(now), sqlc.arg(now));

-- name: LLMChunksByRun :many
SELECT * FROM llm_chunks WHERE stage_run_id = sqlc.arg(stage_run_id) ORDER BY chunk;

-- name: LLMChunk :one
SELECT * FROM llm_chunks WHERE stage_run_id = sqlc.arg(stage_run_id) AND chunk = sqlc.arg(chunk);

-- name: FinishLLMChunk :execrows
-- Only a pending row is filled in: a second write-back of the same chunk matches no row.
UPDATE llm_chunks SET status = sqlc.arg(status), reason = sqlc.arg(reason), output_json = sqlc.narg(output_json),
  usage_json = sqlc.arg(usage_json), detail_json = sqlc.arg(detail_json), executor = sqlc.arg(executor),
  agent = sqlc.arg(agent), model_reported = sqlc.arg(model_reported), updated_at = sqlc.arg(now)
WHERE stage_run_id = sqlc.arg(stage_run_id) AND chunk = sqlc.arg(chunk) AND status = 'pending';

-- name: CountPendingLLMChunks :one
SELECT COUNT(*) FROM llm_chunks WHERE stage_run_id = sqlc.arg(stage_run_id) AND status = 'pending';

-- name: DoneLLMChunkByCacheKey :one
-- The most recent validated output for the same chunk input (LLM-R8). Fallback rows are never reused,
-- and neither are agent outputs: the model an agent reports is not verified (AGT-R10), so a later run
-- must not pass them off as the configured provider's.
SELECT * FROM llm_chunks
WHERE item_id = sqlc.arg(item_id) AND stage = sqlc.arg(stage) AND cache_key = sqlc.arg(cache_key) AND status = 'done'
  AND executor = 'api'
ORDER BY updated_at DESC, rowid DESC LIMIT 1;

-- name: LatestLLMChunkNumbers :many
-- The chunk numbers present in the most recent split of the stage for the item (none when it was never split).
-- Numbers are not always 1..COUNT: translate numbers its chunks globally across targets, and a reused target
-- produces no chunk rows, which leaves holes.
SELECT chunk FROM llm_chunks
WHERE stage_run_id = (
  SELECT c.stage_run_id FROM llm_chunks c
  WHERE c.item_id = sqlc.arg(item_id) AND c.stage = sqlc.arg(stage)
  ORDER BY c.created_at DESC, c.rowid DESC LIMIT 1
)
ORDER BY chunk;
