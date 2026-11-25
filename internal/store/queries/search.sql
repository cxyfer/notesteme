-- name: SearchIndexBuilt :one
SELECT built FROM search_index_state WHERE id = 1;

-- name: SetSearchIndexBuilt :exec
UPDATE search_index_state SET built = 1 WHERE id = 1;

-- name: DeleteSearchCuesForLang :exec
DELETE FROM search_cues WHERE item_id = sqlc.arg(item_id) AND lang = sqlc.arg(lang);

-- name: DeleteSearchCuesForItem :exec
DELETE FROM search_cues WHERE item_id = sqlc.arg(item_id);

-- name: DeleteAllSearchCues :exec
DELETE FROM search_cues;

-- name: InsertSearchCue :exec
-- The store assigns ids itself: a group's cues are contiguous, cue n at first_id + n - 1 (SRCH S9).
INSERT INTO search_cues (id, item_id, artifact_id, lang, variant, cue, start_ms, end_ms, text, text_fold)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(artifact_id), sqlc.arg(lang), sqlc.arg(variant), sqlc.arg(cue),
        sqlc.arg(start_ms), sqlc.arg(end_ms), sqlc.arg(text), sqlc.arg(text_fold));

-- name: NextSearchCueID :one
SELECT CAST(COALESCE(MAX(id), 0) + 1 AS INTEGER) FROM search_cues;

-- name: InsertSearchGroup :exec
INSERT INTO search_groups (first_id, cues, item_id, artifact_id, lang)
VALUES (sqlc.arg(first_id), sqlc.arg(cues), sqlc.arg(item_id), sqlc.arg(artifact_id), sqlc.arg(lang));

-- name: InsertSearchText :exec
INSERT INTO search_text (first_id, fold) VALUES (sqlc.arg(first_id), sqlc.arg(fold));

-- name: DeleteSearchGroupsForLang :exec
DELETE FROM search_groups WHERE item_id = sqlc.arg(item_id) AND lang = sqlc.arg(lang);

-- name: DeleteSearchGroupsForItem :exec
DELETE FROM search_groups WHERE item_id = sqlc.arg(item_id);

-- name: DeleteAllSearchGroups :exec
DELETE FROM search_groups;

-- name: SearchGroupsBuilt :one
SELECT groups_built FROM search_index_state WHERE id = 1;

-- name: SetSearchGroupsBuilt :exec
UPDATE search_index_state SET groups_built = 1 WHERE id = 1;

-- name: CountSearchCuesForItem :one
SELECT COUNT(*) FROM search_cues WHERE item_id = sqlc.arg(item_id);

-- name: CurrentTranscriptsForLang :many
-- Current transcript artifacts of one language, any variant.
SELECT * FROM artifacts
WHERE item_id = sqlc.arg(item_id) AND kind = 'transcript' AND lang = sqlc.arg(lang) AND superseded_by IS NULL
ORDER BY created_at, id;

-- name: CurrentTranscriptKeys :many
-- Every (item, lang) that has a current transcript; the rebuild walks these.
SELECT DISTINCT item_id, lang FROM artifacts
WHERE kind = 'transcript' AND superseded_by IS NULL
ORDER BY item_id, lang;

-- name: SeedItemCreatedAt :execrows
-- Test-only: the acceptance harness pins created_at so that "newer first" (SRCH-R7) is deterministic.
UPDATE items SET created_at = sqlc.arg(created_at) WHERE id = sqlc.arg(id);
