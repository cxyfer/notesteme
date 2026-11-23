-- name: ListLatestNoteTemplates :many
-- The latest version of every template, by id.
SELECT * FROM note_templates t
WHERE t.version = (SELECT MAX(v.version) FROM note_templates v WHERE v.id = t.id)
ORDER BY t.id;

-- name: LatestNoteTemplate :one
SELECT * FROM note_templates WHERE id = sqlc.arg(id) ORDER BY version DESC LIMIT 1;

-- name: GetNoteTemplateVersion :one
SELECT * FROM note_templates WHERE id = sqlc.arg(id) AND version = sqlc.arg(version);

-- name: InsertNoteTemplate :exec
INSERT INTO note_templates (id, version, name, builtin, body, rules_json, sha256, created_at)
VALUES (sqlc.arg(id), sqlc.arg(version), sqlc.arg(name), 0, sqlc.arg(body), sqlc.arg(rules_json), sqlc.arg(sha256), sqlc.arg(now));
