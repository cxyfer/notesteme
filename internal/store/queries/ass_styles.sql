-- name: ListAssStyles :many
SELECT * FROM ass_styles ORDER BY name;

-- name: GetAssStyle :one
SELECT * FROM ass_styles WHERE name = sqlc.arg(name);

-- name: InsertAssStyle :exec
INSERT INTO ass_styles (name, style_json, created_at, updated_at)
VALUES (sqlc.arg(name), sqlc.arg(style_json), sqlc.arg(now), sqlc.arg(now));

-- name: UpdateAssStyle :execrows
UPDATE ass_styles SET style_json = sqlc.arg(style_json), updated_at = sqlc.arg(now)
WHERE name = sqlc.arg(name);
