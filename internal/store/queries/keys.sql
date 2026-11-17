-- name: ActiveWrappedKey :one
SELECT wrapped_key FROM encryption_keys WHERE retired_at IS NULL;

-- name: InsertWrappedKey :execrows
-- Insert only when there is no active key; the rows affected tells whether it was inserted.
-- CAST lets sqlc infer the types of parameters in a SELECT list.
INSERT INTO encryption_keys (id, wrapped_key, created_at)
SELECT CAST(sqlc.arg(id) AS TEXT), CAST(sqlc.arg(wrapped_key) AS TEXT), CAST(sqlc.arg(created_at) AS INTEGER)
WHERE NOT EXISTS (SELECT 1 FROM encryption_keys WHERE retired_at IS NULL);

-- name: RetireActiveKey :execrows
UPDATE encryption_keys
SET retired_at = CAST(sqlc.arg(retired_at) AS INTEGER)
WHERE retired_at IS NULL;

-- name: InsertActiveKey :exec
INSERT INTO encryption_keys (id, wrapped_key, created_at)
VALUES (sqlc.arg(id), sqlc.arg(wrapped_key), sqlc.arg(created_at));

-- name: DeleteAllEncryptionKeys :exec
DELETE FROM encryption_keys;
