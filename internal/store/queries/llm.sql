-- name: InsertLLMChunk :exec
INSERT INTO llm_chunks (stage_run_id, chunk, item_id, stage, cache_key, status, reason, plan_json,
                        output_json, usage_json, reused, created_at, updated_at)
VALUES (sqlc.arg(stage_run_id), sqlc.arg(chunk), sqlc.arg(item_id), sqlc.arg(stage), sqlc.arg(cache_key),
        sqlc.arg(status), sqlc.arg(reason), sqlc.arg(plan_json), sqlc.narg(output_json), sqlc.arg(usage_json),
        sqlc.arg(reused), sqlc.arg(now), sqlc.arg(now));

-- name: LLMChunksByRun :many
SELECT * FROM llm_chunks WHERE stage_run_id = sqlc.arg(stage_run_id) ORDER BY chunk;

-- name: LLMChunk :one
SELECT * FROM llm_chunks WHERE stage_run_id = sqlc.arg(stage_run_id) AND chunk = sqlc.arg(chunk);

-- name: FinishLLMChunk :execrows
-- Only a pending row is filled in: a second write-back of the same chunk matches no row.
UPDATE llm_chunks SET status = sqlc.arg(status), reason = sqlc.arg(reason), output_json = sqlc.narg(output_json),
  usage_json = sqlc.arg(usage_json), updated_at = sqlc.arg(now)
WHERE stage_run_id = sqlc.arg(stage_run_id) AND chunk = sqlc.arg(chunk) AND status = 'pending';

-- name: CountPendingLLMChunks :one
SELECT COUNT(*) FROM llm_chunks WHERE stage_run_id = sqlc.arg(stage_run_id) AND status = 'pending';

-- name: DoneLLMChunkByCacheKey :one
-- The most recent validated output for the same chunk input (LLM-R8). Fallback rows are never reused.
SELECT * FROM llm_chunks
WHERE item_id = sqlc.arg(item_id) AND stage = sqlc.arg(stage) AND cache_key = sqlc.arg(cache_key) AND status = 'done'
ORDER BY updated_at DESC, rowid DESC LIMIT 1;
