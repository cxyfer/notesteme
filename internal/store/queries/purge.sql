-- name: PurgeTrashedIDs :many
-- Every item that is in the trash, oldest deletion first.
SELECT id FROM items WHERE deleted_at IS NOT NULL ORDER BY deleted_at, rowid;

-- name: PurgeExpiredIDs :many
-- Items that were deleted at or before the cutoff (unix milliseconds).
SELECT id FROM items
WHERE deleted_at IS NOT NULL AND deleted_at <= sqlc.arg(cutoff)
ORDER BY deleted_at, rowid;

-- name: PurgeDetachCost :execrows
-- The ledger outlives its item: keep the title and drop the link.
UPDATE cost_ledger SET item_title = sqlc.arg(item_title), item_id = NULL WHERE item_id = sqlc.arg(item_id);

-- name: PurgeUpsertTombstone :exec
-- Platform items have no sha256, so the key ends with an empty string.
INSERT INTO item_tombstones (platform, external_id, part, sha256, purged_at)
VALUES (sqlc.arg(platform), sqlc.arg(external_id), sqlc.arg(part), '', sqlc.arg(purged_at))
ON CONFLICT (platform, external_id, part, sha256) DO UPDATE SET purged_at = excluded.purged_at;

-- name: PurgeDeleteEvents :execrows
-- The item_id <> '' term matches the partial index events_item, which SQLite only uses when the query implies it.
DELETE FROM events WHERE item_id = sqlc.arg(item_id) AND item_id <> '';

-- name: PurgeDeleteItem :execrows
-- Artifacts, stage runs, source probes and tasks go with it (ON DELETE CASCADE).
DELETE FROM items WHERE id = sqlc.arg(id);

-- name: PurgeDeleteOrphanSnapshots :execrows
-- A snapshot that no stage run points to any more is no longer needed.
DELETE FROM config_snapshots WHERE id NOT IN (SELECT config_snapshot_id FROM stage_runs);

-- name: PreviewArtifactStats :one
-- All versions count, superseded ones included.
SELECT COUNT(*) AS artifacts, CAST(COALESCE(SUM(size), 0) AS INTEGER) AS bytes
FROM artifacts WHERE item_id = sqlc.arg(item_id);

-- name: PreviewSpentUSD :one
-- Only money that was really spent counts, not reservations or unknown amounts.
SELECT CAST(COALESCE(SUM(usd), 0) AS REAL) AS spent_usd
FROM cost_ledger WHERE item_id = sqlc.arg(item_id) AND state = 'actual';

-- name: InsertCostEntry :exec
INSERT INTO cost_ledger (id, item_id, item_title, provider, model, units, unit_type, usd, state, at)
VALUES (sqlc.arg(id), sqlc.narg(item_id), sqlc.arg(item_title), sqlc.arg(provider), sqlc.arg(model),
        sqlc.arg(units), sqlc.arg(unit_type), sqlc.arg(usd), sqlc.arg(state), sqlc.arg(at));

-- name: ListCostEntries :many
SELECT * FROM cost_ledger ORDER BY at, rowid;
