-- name: ItemByLocalSHA :one
-- Only local items whose content has been hashed (the items_local_sha partial index).
-- Items that are not in the trash come first, then oldest first so the result is stable.
SELECT * FROM items
WHERE platform = 'local' AND content_sha256 <> '' AND content_sha256 = sqlc.arg(sha)
ORDER BY (deleted_at IS NOT NULL), created_at, rowid
LIMIT 1;

-- name: LocalTombstoneBlocks :one
-- A purged local file blocks re-adding when the content matches, or when the same key was purged
-- before its content was ever hashed (tombstone sha256 is empty).
SELECT EXISTS (
  SELECT 1 FROM item_tombstones
  WHERE platform = 'local'
    AND ((sha256 <> '' AND sha256 = sqlc.arg(sha))
         OR (external_id = sqlc.arg(external_id) AND part = 0 AND sha256 = ''))
) AS blocked;

-- name: SetItemContentSHA :exec
-- Write once: an item that already has a hash keeps it.
UPDATE items SET content_sha256 = sqlc.arg(sha) WHERE id = sqlc.arg(id) AND content_sha256 = '';

-- name: ListInboxEntries :many
SELECT * FROM inbox_entries ORDER BY path;

-- name: UpsertInboxEntry :exec
INSERT INTO inbox_entries (path, size, mtime_ns, sha256, state, reason, item_id, decided_at)
VALUES (sqlc.arg(path), sqlc.arg(size), sqlc.arg(mtime_ns), sqlc.arg(sha256), sqlc.arg(state), sqlc.arg(reason),
        sqlc.narg(item_id), sqlc.arg(decided_at))
ON CONFLICT (path) DO UPDATE SET
  size = excluded.size, mtime_ns = excluded.mtime_ns, sha256 = excluded.sha256, state = excluded.state,
  reason = excluded.reason, item_id = excluded.item_id, decided_at = excluded.decided_at;

-- name: DeleteInboxEntry :exec
DELETE FROM inbox_entries WHERE path = sqlc.arg(path);

-- name: DeleteLocalTombstonesBySHA :execrows
-- An upload of the same content removes the local tombstones that would block it, whatever the old file name was.
DELETE FROM item_tombstones WHERE platform = 'local' AND sha256 <> '' AND sha256 = sqlc.arg(sha);
