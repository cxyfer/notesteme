-- name: InsertEvent :exec
INSERT INTO events (id, item_id, type, payload_json, at)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(type), sqlc.arg(payload_json), sqlc.arg(at));

-- name: CountEvents :one
SELECT COUNT(*) FROM events;

-- name: DeleteEventsBefore :execrows
DELETE FROM events WHERE at < sqlc.arg(cutoff);
