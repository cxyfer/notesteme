-- name: InsertItemIfAbsent :execrows
INSERT INTO items (id, platform, external_id, part, profile_id, overrides_json, until_stage, url, created_at)
VALUES (sqlc.arg(id), sqlc.arg(platform), sqlc.arg(external_id), sqlc.arg(part), NULLIF(CAST(sqlc.arg(profile_id) AS TEXT), ''),
        sqlc.arg(overrides_json), sqlc.arg(until_stage), sqlc.arg(url), sqlc.arg(created_at))
ON CONFLICT (platform, external_id, part) DO NOTHING;

-- name: ItemByID :one
SELECT * FROM items WHERE id = sqlc.arg(id);

-- name: ItemByKey :one
SELECT * FROM items WHERE platform = sqlc.arg(platform) AND external_id = sqlc.arg(external_id) AND part = sqlc.arg(part);

-- name: ListItems :many
SELECT * FROM items WHERE deleted_at IS NULL ORDER BY created_at, rowid;

-- name: UpdateItemMeta :execrows
-- Empty string and NULL mean the platform gave nothing: keep the old value.
-- lang_source is only written when original_lang is non-empty.
UPDATE items SET
  title         = COALESCE(NULLIF(CAST(sqlc.arg(title) AS TEXT), ''), title),
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
