-- Saved views of the library (LIB-R26 to R28).

-- name: InsertSavedView :exec
INSERT INTO saved_views (id, name, name_fold, query_json, created_at)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(name_fold), sqlc.arg(query_json), sqlc.arg(created_at));

-- name: ListSavedViews :many
-- In creation order; rowid breaks ties of the same created_at.
SELECT id, name, query_json, created_at FROM saved_views ORDER BY created_at, rowid;

-- name: CountSavedViews :one
SELECT CAST(COUNT(*) AS INTEGER) FROM saved_views;

-- name: DeleteSavedView :execrows
DELETE FROM saved_views WHERE id = sqlc.arg(id);
