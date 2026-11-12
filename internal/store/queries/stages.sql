-- name: GetSnapshotIDBySHA :one
SELECT id FROM config_snapshots WHERE sha256 = sqlc.arg(sha256);

-- name: InsertSnapshot :exec
INSERT INTO config_snapshots (id, sha256, config_json, created_at)
VALUES (sqlc.arg(id), sqlc.arg(sha256), sqlc.arg(config_json), sqlc.arg(created_at));

-- name: SnapshotConfig :one
SELECT config_json FROM config_snapshots WHERE id = sqlc.arg(id);

-- name: InsertStageRun :exec
INSERT INTO stage_runs (id, item_id, stage, lang, variant, status, config_snapshot_id, created_at)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(stage), sqlc.arg(lang), sqlc.arg(variant), sqlc.arg(status),
        sqlc.arg(config_snapshot_id), sqlc.arg(created_at));

-- name: StageRunByID :one
SELECT * FROM stage_runs WHERE id = sqlc.arg(id);

-- name: StageRunsByItem :many
SELECT * FROM stage_runs WHERE item_id = sqlc.arg(item_id) ORDER BY created_at, rowid;

-- name: SetStageState :one
-- Every descriptive column is written (unset means cleared); started_at only changes when given,
-- finished_at is always overwritten.
UPDATE stage_runs SET
  status        = sqlc.arg(status),
  block_reason  = sqlc.arg(block_reason),
  error_class   = sqlc.arg(error_class),
  error_message = sqlc.arg(error_message),
  actions_json  = sqlc.arg(actions_json),
  not_before    = sqlc.narg(not_before),
  started_at    = COALESCE(sqlc.narg(started_at), started_at),
  finished_at   = sqlc.narg(finished_at)
WHERE id = sqlc.arg(id)
RETURNING item_id, stage, status;

-- name: SetStageResult :exec
UPDATE stage_runs SET
  executor       = sqlc.arg(executor),
  cache_key      = sqlc.arg(cache_key),
  impl_version   = sqlc.arg(impl_version),
  artifact_id    = NULLIF(CAST(sqlc.arg(artifact_id) AS TEXT), '')
WHERE id = sqlc.arg(id);

-- name: DoneStageByCacheKey :one
SELECT * FROM stage_runs
WHERE item_id = sqlc.arg(item_id) AND stage = sqlc.arg(stage) AND cache_key = sqlc.arg(cache_key) AND status = 'done'
ORDER BY created_at DESC, rowid DESC LIMIT 1;

-- name: MarkStageRunning :one
-- Claiming a task marks its stage as running and clears the previous message and finish time.
UPDATE stage_runs SET status = 'running', block_reason = '', error_class = '', error_message = '',
  actions_json = '[]', not_before = NULL, started_at = CAST(sqlc.arg(started_at) AS INTEGER), finished_at = NULL
WHERE id = sqlc.arg(id)
RETURNING item_id, stage, status;

-- name: ResetRunningStage :one
-- When a lease goes back to the queue, a stage still running returns to pending (no row when it was not running).
UPDATE stage_runs SET status = 'pending' WHERE id = sqlc.arg(id) AND status = 'running'
RETURNING item_id, stage, status;
