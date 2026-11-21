-- name: ListPrompts :many
SELECT * FROM prompts ORDER BY id;

-- name: GetPrompt :one
SELECT * FROM prompts WHERE id = sqlc.arg(id);

-- name: InsertPrompt :exec
-- A new prompt starts at version 1, which is also its default version.
INSERT INTO prompts (id, kind, default_version, latest_version, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(kind), 1, 1, sqlc.arg(now), sqlc.arg(now));

-- name: BumpPromptLatest :one
-- Adding a version never touches default_version (PRM-R5).
UPDATE prompts SET latest_version = latest_version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
RETURNING latest_version;

-- name: SetPromptDefault :execrows
-- Only succeeds when the version exists.
UPDATE prompts SET default_version = sqlc.arg(version), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND EXISTS (SELECT 1 FROM prompt_versions pv WHERE pv.prompt_id = sqlc.arg(id) AND pv.version = sqlc.arg(version));

-- name: InsertPromptVersion :exec
INSERT INTO prompt_versions (prompt_id, version, body, sha256, variables_json, created_at)
VALUES (sqlc.arg(prompt_id), sqlc.arg(version), sqlc.arg(body), sqlc.arg(sha256), sqlc.arg(variables_json), sqlc.arg(now));

-- name: GetPromptVersion :one
SELECT * FROM prompt_versions WHERE prompt_id = sqlc.arg(prompt_id) AND version = sqlc.arg(version);

-- name: ListPromptVersions :many
SELECT * FROM prompt_versions WHERE prompt_id = sqlc.arg(prompt_id) ORDER BY version;

-- name: DeletePrompt :execrows
-- Versions go with it (ON DELETE CASCADE).
DELETE FROM prompts WHERE id = sqlc.arg(id);
