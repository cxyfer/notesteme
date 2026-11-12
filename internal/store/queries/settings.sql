-- name: GetSetting :one
SELECT value FROM settings WHERE key = sqlc.arg(key);

-- name: SetSetting :exec
INSERT INTO settings (key, value, updated_at) VALUES (sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at))
ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: SetSettingIfAbsent :execrows
INSERT INTO settings (key, value, updated_at) VALUES (sqlc.arg(key), sqlc.arg(value), sqlc.arg(updated_at))
ON CONFLICT (key) DO NOTHING;
