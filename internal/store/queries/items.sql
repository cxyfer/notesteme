-- name: InsertItemIfAbsent :execrows
INSERT INTO items (id, platform, external_id, part, profile_id, overrides_json, until_stage, url, source_path, content_sha256, origin, created_at)
VALUES (sqlc.arg(id), sqlc.arg(platform), sqlc.arg(external_id), sqlc.arg(part), NULLIF(CAST(sqlc.arg(profile_id) AS TEXT), ''),
        sqlc.arg(overrides_json), sqlc.arg(until_stage), sqlc.arg(url), sqlc.arg(source_path), sqlc.arg(content_sha256),
        sqlc.arg(origin), sqlc.arg(created_at))
ON CONFLICT (platform, external_id, part) DO NOTHING;

-- name: ItemByID :one
SELECT * FROM items WHERE id = sqlc.arg(id);

-- name: ItemByKey :one
SELECT * FROM items WHERE platform = sqlc.arg(platform) AND external_id = sqlc.arg(external_id) AND part = sqlc.arg(part);

-- name: ListItems :many
SELECT * FROM items WHERE deleted_at IS NULL ORDER BY created_at, rowid;

-- name: UpdateItemMeta :execrows
-- Empty string and NULL mean the platform gave nothing: keep the old value.
-- title_fold is the Go-lowercased title, written together with it.
-- lang_source is only written when original_lang is non-empty.
-- media_kind and content_sha256 follow the same rule: an empty string keeps the old value.
UPDATE items SET
  media_kind     = COALESCE(NULLIF(CAST(sqlc.arg(media_kind) AS TEXT), ''), media_kind),
  content_sha256 = COALESCE(NULLIF(CAST(sqlc.arg(content_sha256) AS TEXT), ''), content_sha256),
  title         = COALESCE(NULLIF(CAST(sqlc.arg(title) AS TEXT), ''), title),
  title_fold    = COALESCE(NULLIF(CAST(sqlc.arg(title_fold) AS TEXT), ''), title_fold),
  duration_s    = COALESCE(sqlc.narg(duration_s), duration_s),
  published_at  = COALESCE(sqlc.narg(published_at), published_at),
  original_lang = COALESCE(NULLIF(CAST(sqlc.arg(original_lang) AS TEXT), ''), original_lang),
  lang_source   = CASE WHEN CAST(sqlc.arg(original_lang) AS TEXT) <> '' THEN CAST(sqlc.arg(lang_source) AS TEXT) ELSE lang_source END,
  availability  = COALESCE(NULLIF(CAST(sqlc.arg(availability) AS TEXT), ''), availability),
  channel_id    = COALESCE(NULLIF(CAST(sqlc.arg(channel_id) AS TEXT), ''), channel_id),
  metadata_json = sqlc.arg(metadata_json)
WHERE id = sqlc.arg(id);

-- name: UpsertChannel :one
INSERT INTO channels (id, platform, external_id, title, url, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(platform), sqlc.arg(external_id), sqlc.arg(title), sqlc.arg(url), sqlc.arg(updated_at))
ON CONFLICT (platform, external_id) DO UPDATE SET
  title = COALESCE(NULLIF(excluded.title, ''), title),
  url = COALESCE(NULLIF(excluded.url, ''), url),
  updated_at = excluded.updated_at
RETURNING id;

-- name: SetItemUntil :execrows
UPDATE items SET until_stage = sqlc.arg(until_stage) WHERE id = sqlc.arg(id);

-- name: ListItemsPage :many
-- The library list (LIB-R1, R3, R4, R7). Keep the WHERE block identical to CountItemsFiltered.
-- status_filters is a JSON array of {"stage": ..., "statuses": "|a|b|"}: values of one stage are OR, stages are AND,
-- and a stage counts by its latest run (a stage without any run counts as 'pending'). q_fold is already lowercased by Go. Without a cursor, after_created_at is
-- the maximum int64 and after_id is empty. Keyset order is (created_at, id) descending.
WITH sf AS (
  SELECT json_extract(j.value, '$.stage') AS stage, json_extract(j.value, '$.statuses') AS statuses
  FROM json_each(CAST(sqlc.arg(status_filters) AS TEXT)) AS j
)
SELECT * FROM items
WHERE deleted_at IS NULL
  AND (CAST(sqlc.arg(platform) AS TEXT) = '' OR platform = sqlc.arg(platform))
  AND (CAST(sqlc.arg(channel_id) AS TEXT) = '' OR channel_id = sqlc.arg(channel_id))
  AND (CAST(sqlc.arg(lang) AS TEXT) = '' OR lower(original_lang) = lower(sqlc.arg(lang)))
  AND (CAST(sqlc.narg(published_from) AS INTEGER) IS NULL OR published_at >= CAST(sqlc.narg(published_from) AS INTEGER))
  AND (CAST(sqlc.narg(published_to) AS INTEGER) IS NULL OR published_at <= CAST(sqlc.narg(published_to) AS INTEGER))
  AND (CAST(sqlc.narg(duration_min) AS INTEGER) IS NULL OR duration_s >= CAST(sqlc.narg(duration_min) AS INTEGER))
  AND (CAST(sqlc.narg(duration_max) AS INTEGER) IS NULL OR duration_s <= CAST(sqlc.narg(duration_max) AS INTEGER))
  AND (CAST(sqlc.arg(q_fold) AS TEXT) = '' OR instr(title_fold, sqlc.arg(q_fold)) > 0)
  AND (CAST(sqlc.arg(error_class) AS TEXT) = '' OR EXISTS (
        SELECT 1 FROM stage_runs AS sr
        WHERE sr.item_id = items.id AND sr.error_class = sqlc.arg(error_class)
          AND sr.id = (SELECT r.id FROM stage_runs AS r WHERE r.item_id = sr.item_id AND r.stage = sr.stage
                       ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1)))
  AND (CAST(sqlc.arg(timing) AS TEXT) = '' OR EXISTS (
        SELECT 1 FROM artifacts AS a, json_each(a.timings_json) AS tj
        WHERE a.item_id = items.id AND a.kind = 'transcript' AND a.superseded_by IS NULL AND tj.value = sqlc.arg(timing)))
  AND (json_array_length(CAST(sqlc.arg(status_filters) AS TEXT)) = 0 OR NOT EXISTS (
        SELECT 1 FROM sf
        WHERE COALESCE(instr(sf.statuses,
                '|' || COALESCE((SELECT r.status FROM stage_runs AS r WHERE r.item_id = items.id AND r.stage = sf.stage
                                 ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1), 'pending') || '|'), 0) = 0))
  AND created_at <= CAST(sqlc.arg(after_created_at) AS INTEGER)
  AND (created_at < CAST(sqlc.arg(after_created_at) AS INTEGER) OR id < CAST(sqlc.arg(after_id) AS TEXT))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: CountItemsFiltered :one
-- Total of the same filters, without the keyset condition and the limit. Keep the WHERE block identical to ListItemsPage.
WITH sf AS (
  SELECT json_extract(j.value, '$.stage') AS stage, json_extract(j.value, '$.statuses') AS statuses
  FROM json_each(CAST(sqlc.arg(status_filters) AS TEXT)) AS j
)
SELECT COUNT(*) FROM items
WHERE deleted_at IS NULL
  AND (CAST(sqlc.arg(platform) AS TEXT) = '' OR platform = sqlc.arg(platform))
  AND (CAST(sqlc.arg(channel_id) AS TEXT) = '' OR channel_id = sqlc.arg(channel_id))
  AND (CAST(sqlc.arg(lang) AS TEXT) = '' OR lower(original_lang) = lower(sqlc.arg(lang)))
  AND (CAST(sqlc.narg(published_from) AS INTEGER) IS NULL OR published_at >= CAST(sqlc.narg(published_from) AS INTEGER))
  AND (CAST(sqlc.narg(published_to) AS INTEGER) IS NULL OR published_at <= CAST(sqlc.narg(published_to) AS INTEGER))
  AND (CAST(sqlc.narg(duration_min) AS INTEGER) IS NULL OR duration_s >= CAST(sqlc.narg(duration_min) AS INTEGER))
  AND (CAST(sqlc.narg(duration_max) AS INTEGER) IS NULL OR duration_s <= CAST(sqlc.narg(duration_max) AS INTEGER))
  AND (CAST(sqlc.arg(q_fold) AS TEXT) = '' OR instr(title_fold, sqlc.arg(q_fold)) > 0)
  AND (CAST(sqlc.arg(error_class) AS TEXT) = '' OR EXISTS (
        SELECT 1 FROM stage_runs AS sr
        WHERE sr.item_id = items.id AND sr.error_class = sqlc.arg(error_class)
          AND sr.id = (SELECT r.id FROM stage_runs AS r WHERE r.item_id = sr.item_id AND r.stage = sr.stage
                       ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1)))
  AND (CAST(sqlc.arg(timing) AS TEXT) = '' OR EXISTS (
        SELECT 1 FROM artifacts AS a, json_each(a.timings_json) AS tj
        WHERE a.item_id = items.id AND a.kind = 'transcript' AND a.superseded_by IS NULL AND tj.value = sqlc.arg(timing)))
  AND (json_array_length(CAST(sqlc.arg(status_filters) AS TEXT)) = 0 OR NOT EXISTS (
        SELECT 1 FROM sf
        WHERE COALESCE(instr(sf.statuses,
                '|' || COALESCE((SELECT r.status FROM stage_runs AS r WHERE r.item_id = items.id AND r.stage = sf.stage
                                 ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1), 'pending') || '|'), 0) = 0));
