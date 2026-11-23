-- Digests (DIG-R8, R9). notifier_ids_json is a JSON array of notifier ids.

-- name: InsertDigest :exec
INSERT INTO digests (id, period, at, weekday, timezone, notifier_ids_json, scope, enabled, created_at)
VALUES (sqlc.arg(id), sqlc.arg(period), sqlc.arg(at), sqlc.arg(weekday), sqlc.arg(timezone),
        sqlc.arg(notifier_ids_json), sqlc.arg(scope), sqlc.arg(enabled), sqlc.arg(created_at));

-- name: UpdateDigest :execrows
-- Settings only; created_at is kept. last_run_at is written as given (NULL restarts the range, DIG-R9).
UPDATE digests
SET period = sqlc.arg(period), at = sqlc.arg(at), weekday = sqlc.arg(weekday), timezone = sqlc.arg(timezone),
    notifier_ids_json = sqlc.arg(notifier_ids_json), scope = sqlc.arg(scope), enabled = sqlc.arg(enabled),
    last_run_at = sqlc.narg(last_run_at)
WHERE id = sqlc.arg(id);

-- name: GetDigest :one
SELECT * FROM digests WHERE id = sqlc.arg(id);

-- name: ListDigests :many
SELECT * FROM digests ORDER BY created_at, id;

-- name: DeleteDigest :execrows
DELETE FROM digests WHERE id = sqlc.arg(id);

-- name: SetDigestLastRun :exec
UPDATE digests SET last_run_at = sqlc.arg(last_run_at) WHERE id = sqlc.arg(id);

-- name: InsertDigestEntry :exec
INSERT INTO digest_entries (digest_id, item_id, run_at)
VALUES (sqlc.arg(digest_id), sqlc.arg(item_id), sqlc.arg(run_at))
ON CONFLICT (digest_id, item_id) DO NOTHING;

-- name: DigestCandidates :many
-- Items whose summary stage finished (done or skipped, not cancelled) in (range_start, range_end], not in the
-- trash, not yet listed by this digest; all_items = 0 keeps only items created by a subscription (DIG-R8, R9).
SELECT sr.item_id AS item_id, CAST(MAX(sr.finished_at) AS INTEGER) AS finished_at
FROM stage_runs sr
JOIN items i ON i.id = sr.item_id
WHERE sr.stage = 'summary'
  AND sr.status IN ('done', 'skipped')
  AND sr.block_reason <> 'cancelled'
  AND sr.finished_at > sqlc.arg(range_start)
  AND sr.finished_at <= sqlc.arg(range_end)
  AND i.deleted_at IS NULL
  AND (CAST(sqlc.arg(all_items) AS INTEGER) = 1 OR i.subscription_id IS NOT NULL)
  AND NOT EXISTS (SELECT 1 FROM digest_entries de WHERE de.digest_id = sqlc.arg(digest_id) AND de.item_id = sr.item_id)
GROUP BY sr.item_id
ORDER BY 2, 1;
