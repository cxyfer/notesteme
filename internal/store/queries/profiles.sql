-- name: ListProfiles :many
SELECT * FROM profiles ORDER BY name;

-- name: GetProfile :one
SELECT * FROM profiles WHERE name = sqlc.arg(name);

-- name: InsertProfile :exec
INSERT INTO profiles (name, current_version, builtin, created_at, updated_at)
VALUES (sqlc.arg(name), 1, 0, sqlc.arg(created_at), sqlc.arg(created_at));

-- name: BumpProfileVersion :one
-- Every save adds a version; the new current_version is returned.
UPDATE profiles SET current_version = current_version + 1, updated_at = sqlc.arg(updated_at)
WHERE name = sqlc.arg(name)
RETURNING current_version;

-- name: InsertProfileVersion :exec
INSERT INTO profile_versions (name, version, config_json, sha256, created_at)
VALUES (sqlc.arg(name), sqlc.arg(version), sqlc.arg(config_json), sqlc.arg(sha256), sqlc.arg(created_at));

-- name: GetProfileVersion :one
SELECT * FROM profile_versions WHERE name = sqlc.arg(name) AND version = sqlc.arg(version);

-- name: ListProfileVersions :many
SELECT * FROM profile_versions WHERE name = sqlc.arg(name) ORDER BY version;

-- name: DeleteProfile :execrows
-- The built-in profile is never deleted; versions go with it (ON DELETE CASCADE, foreign_keys is on for every connection).
DELETE FROM profiles WHERE name = sqlc.arg(name) AND builtin = 0;

-- name: CurrentProfileConfigs :many
-- The config of the current version of every profile (used to find who references a prompt).
SELECT p.name, pv.config_json
FROM profiles p
JOIN profile_versions pv ON pv.name = p.name AND pv.version = p.current_version
ORDER BY p.name;
