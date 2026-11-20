-- name: TrashItem :execrows
-- Only an item that is not in the trash yet moves there; zero rows means it is already in the trash.
UPDATE items SET deleted_at = sqlc.arg(deleted_at) WHERE id = sqlc.arg(id) AND deleted_at IS NULL;

-- name: RestoreItem :execrows
-- Only an item that is in the trash comes back; zero rows means it is not in the trash.
UPDATE items SET deleted_at = NULL WHERE id = sqlc.arg(id) AND deleted_at IS NOT NULL;

-- name: ListTrash :many
-- The trash view: newest deletion first.
SELECT * FROM items WHERE deleted_at IS NOT NULL ORDER BY deleted_at DESC, rowid DESC;

-- name: DeleteTombstones :execrows
-- Adding an item by hand removes its tombstone (DEL-R11), whatever the sha256 of the old file was.
DELETE FROM item_tombstones WHERE platform = sqlc.arg(platform) AND external_id = sqlc.arg(external_id) AND part = sqlc.arg(part);
