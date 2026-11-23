-- name: OverviewAttentionRuns :many
-- Latest stage runs that are running, blocked or failed, of items not in the trash (LIB-R21 now and needs_you).
-- The status IN term matches the partial index stage_runs_attention; status_filter narrows it further.
SELECT sr.id, sr.item_id, sr.stage, sr.status, sr.block_reason, sr.error_class, sr.error_message, sr.actions_json,
       sr.started_at, sr.finished_at, sr.created_at,
       i.platform, i.external_id, i.part, i.title
FROM stage_runs AS sr
JOIN items AS i ON i.id = sr.item_id
WHERE sr.status IN ('running', 'blocked', 'failed')
  AND instr(CAST(sqlc.arg(status_filter) AS TEXT), '|' || sr.status || '|') > 0
  AND i.deleted_at IS NULL
  AND sr.id = (SELECT r.id FROM stage_runs AS r WHERE r.item_id = sr.item_id AND r.stage = sr.stage
               ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1)
ORDER BY sr.rowid;

-- name: StageRunTaskProgress :one
-- Tasks of one stage run: how many are done out of all non-cancelled ones (LIB-R21 progress).
SELECT CAST(COALESCE(SUM(CASE WHEN state = 'done' THEN 1 ELSE 0 END), 0) AS INTEGER) AS done,
       CAST(COUNT(*) AS INTEGER) AS total
FROM tasks WHERE stage_run_id = sqlc.arg(stage_run_id) AND state != 'cancelled';

-- name: OverviewEvents :many
-- Events since a moment, newest first, of the types the overview lists (LIB-R21 today). stage.status only when the
-- new status is done, failed or blocked. Events of items now in the trash are left out, except item.deleted.
SELECT e.type, e.payload_json, e.at, CAST(COALESCE(i.title, '') AS TEXT) AS title
FROM events AS e
LEFT JOIN items AS i ON i.id = e.item_id
WHERE e.at >= sqlc.arg(since)
  AND e.type IN ('item.created', 'item.deleted', 'item.restored', 'item.purged', 'items.batch', 'stage.status',
                 'budget.warning', 'notify.dropped', 'subscription.created', 'subscription.checked',
                 'subscription.check_failed', 'subscription.deleted')
  AND (e.type != 'stage.status' OR json_extract(e.payload_json, '$.status') IN ('done', 'failed', 'blocked'))
  AND (i.id IS NULL OR i.deleted_at IS NULL OR e.type = 'item.deleted')
ORDER BY e.at DESC, e.rowid DESC
LIMIT sqlc.arg(page_size);

-- name: OverviewRecentDone :many
-- Items not in the trash whose latest run of every stage is done or skipped, by their last finish (LIB-R21 recent_done).
-- Only the latest scan_limit finished runs are considered, so the query stays bounded on a large library.
WITH recent AS (
  SELECT f.item_id, MAX(f.finished_at) AS done_at
  FROM (SELECT item_id, finished_at FROM stage_runs WHERE finished_at IS NOT NULL
        ORDER BY finished_at DESC LIMIT sqlc.arg(scan_limit)) AS f
  GROUP BY f.item_id
)
SELECT i.id, i.platform, i.external_id, i.part, i.title, CAST(recent.done_at AS INTEGER) AS done_at
FROM recent
JOIN items AS i ON i.id = recent.item_id
WHERE i.deleted_at IS NULL
  AND NOT EXISTS (
        SELECT 1 FROM stage_runs AS sr
        WHERE sr.item_id = i.id AND sr.status NOT IN ('done', 'skipped')
          AND sr.id = (SELECT r.id FROM stage_runs AS r WHERE r.item_id = sr.item_id AND r.stage = sr.stage
                       ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1))
ORDER BY recent.done_at DESC, i.id DESC
LIMIT sqlc.arg(page_size);
