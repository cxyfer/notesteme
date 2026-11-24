-- name: ListAlertStates :many
-- Every alert row, active ones first, then by the time the condition started.
SELECT * FROM alert_states ORDER BY (resolved_at IS NOT NULL), active_since, kind, key;

-- name: ActivateAlert :exec
-- Start (or restart after a resolution) an alert: the notification time and the resolution are cleared.
INSERT INTO alert_states (kind, key, message, active_since, notified_at, resolved_at)
VALUES (sqlc.arg(kind), sqlc.arg(key), sqlc.arg(message), sqlc.arg(since), NULL, NULL)
ON CONFLICT (kind, key) DO UPDATE SET
  message = excluded.message, active_since = excluded.active_since, notified_at = NULL, resolved_at = NULL;

-- name: SetAlertMessage :exec
-- The condition still holds: only the text changes (for example the disk usage percentage).
UPDATE alert_states SET message = sqlc.arg(message) WHERE kind = sqlc.arg(kind) AND key = sqlc.arg(key);

-- name: MarkAlertNotified :exec
UPDATE alert_states SET notified_at = sqlc.arg(at) WHERE kind = sqlc.arg(kind) AND key = sqlc.arg(key);

-- name: ResolveAlert :execrows
-- Only an active alert can be resolved.
UPDATE alert_states SET resolved_at = sqlc.arg(at)
WHERE kind = sqlc.arg(kind) AND key = sqlc.arg(key) AND resolved_at IS NULL;

-- name: AddToolCall :exec
-- Count one call in its 5-minute bucket (OPS-R18).
INSERT INTO tool_call_stats (tool, bucket_start, calls, failures)
VALUES (sqlc.arg(tool), sqlc.arg(bucket_start), 1, sqlc.arg(failures))
ON CONFLICT (tool, bucket_start) DO UPDATE SET
  calls = calls + 1, failures = failures + excluded.failures;

-- name: SetToolCalls :exec
-- Test hook: replace the counts of one bucket.
INSERT INTO tool_call_stats (tool, bucket_start, calls, failures)
VALUES (sqlc.arg(tool), sqlc.arg(bucket_start), sqlc.arg(calls), sqlc.arg(failures))
ON CONFLICT (tool, bucket_start) DO UPDATE SET calls = excluded.calls, failures = excluded.failures;

-- name: ToolCallTotals :one
-- Calls and failures of one tool in the buckets that start at or after since.
SELECT CAST(COALESCE(SUM(calls), 0) AS INTEGER) AS calls, CAST(COALESCE(SUM(failures), 0) AS INTEGER) AS failures
FROM tool_call_stats WHERE tool = sqlc.arg(tool) AND bucket_start >= sqlc.arg(since);

-- name: PruneToolCallStats :execrows
DELETE FROM tool_call_stats WHERE bucket_start < sqlc.arg(before);

-- name: MarkSecretOK :exec
-- A yt-dlp call that used this cookie exited with 0 (OPS-R17). It does not clear expired_at (SEC-R12):
-- many calls succeed without signing in, so only a new upload does.
UPDATE secrets SET last_used_at = sqlc.arg(at), last_ok_at = sqlc.arg(at)
WHERE kind = sqlc.arg(kind) AND name = sqlc.arg(name);

-- name: MarkSecretUsed :exec
-- A yt-dlp call that used this cookie failed for a reason that says nothing about the cookie (SEC-R12).
UPDATE secrets SET last_used_at = sqlc.arg(at) WHERE kind = sqlc.arg(kind) AND name = sqlc.arg(name);

-- name: MarkSecretExpired :exec
-- A yt-dlp call that used this cookie was told to sign in: the cookie no longer logs in (SEC-R12).
UPDATE secrets SET last_used_at = sqlc.arg(at), expired_at = sqlc.arg(at)
WHERE kind = sqlc.arg(kind) AND name = sqlc.arg(name);

-- name: SecretStatus :many
-- The status times of the secrets of one kind (SEC-R12); never the nonce or the ciphertext.
SELECT name, updated_at, last_used_at, last_ok_at, expired_at FROM secrets WHERE kind = sqlc.arg(kind) ORDER BY name;

-- name: SetSecretTimes :execrows
-- Test hook: the upload time and the last success of a secret.
UPDATE secrets SET created_at = sqlc.arg(at), updated_at = sqlc.arg(at), last_ok_at = sqlc.arg(at)
WHERE kind = sqlc.arg(kind) AND name = sqlc.arg(name);

-- name: SecretFreshness :many
-- Upload and last success times of the secrets of one kind (never the ciphertext).
SELECT name, updated_at, last_ok_at FROM secrets WHERE kind = sqlc.arg(kind) ORDER BY name;

-- name: FailingScheduledJobs :many
-- Scheduled jobs whose last run failed (OPS-R15).
SELECT * FROM scheduled_jobs WHERE failures > 0 ORDER BY kind, key;

-- name: ActiveStageRuns :many
-- Stage runs that are queued or running with at least one unfinished task (OPS-R14), with
-- the newest task creation time, the latest retry time and the resource classes of their tasks.
SELECT sr.id, sr.item_id, sr.stage, sr.status, sr.started_at, sr.created_at,
  CAST(MAX(t.created_at) AS INTEGER) AS task_created_at,
  CAST(MAX(COALESCE(t.not_before, 0)) AS INTEGER) AS task_not_before,
  CAST(GROUP_CONCAT(DISTINCT t.resource_class) AS TEXT) AS classes
FROM stage_runs sr
JOIN items i ON i.id = sr.item_id AND i.deleted_at IS NULL
JOIN tasks t ON t.stage_run_id = sr.id AND t.state IN ('pending', 'leased')
WHERE sr.status IN ('pending', 'running', 'awaiting_agent')
GROUP BY sr.id
ORDER BY sr.item_id, sr.created_at, sr.rowid;
