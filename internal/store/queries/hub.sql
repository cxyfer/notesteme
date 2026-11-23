-- Client side of the Hub (HSHR).

-- name: InsertHubShare :exec
INSERT INTO hub_shares (id, item_id, hub_url, share_id, versions_json, created_at)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(hub_url), sqlc.arg(share_id), sqlc.arg(versions_json), sqlc.arg(created_at));

-- name: ItemHubShares :many
SELECT * FROM hub_shares WHERE item_id = sqlc.arg(item_id) ORDER BY created_at, id;

-- name: UpsertHubImport :exec
-- A newer import for the same item replaces the pending one.
INSERT INTO hub_imports (item_id, version_id, sha256, origin_json, content, created_at)
VALUES (sqlc.arg(item_id), sqlc.arg(version_id), sqlc.arg(sha256), sqlc.arg(origin_json), sqlc.arg(content), sqlc.arg(created_at))
ON CONFLICT (item_id) DO UPDATE SET version_id = excluded.version_id, sha256 = excluded.sha256,
    origin_json = excluded.origin_json, content = excluded.content, created_at = excluded.created_at;

-- name: GetHubImport :one
SELECT * FROM hub_imports WHERE item_id = sqlc.arg(item_id);

-- name: DeleteHubImport :exec
-- Only the import that was used: a newer one (different sha256) stays pending.
DELETE FROM hub_imports WHERE item_id = sqlc.arg(item_id) AND sha256 = sqlc.arg(sha256);
