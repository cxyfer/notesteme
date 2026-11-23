-- name: ListVersions :many
-- by_score is 1 (sort by score) or 0 (newest first); visibilities_json is a JSON array of strings.
-- Visible versions that match the filters and that the viewer may see, in keyset order
-- (score times by_score, then created_at, then id, all descending). Empty filter values mean "any".
SELECT sqlc.embed(v), sqlc.embed(vd), CAST(v.score * CAST(sqlc.arg(by_score) AS INTEGER) AS INTEGER) AS sort_score
FROM versions v JOIN videos vd ON vd.id = v.video_id
WHERE v.state = 'visible'
  AND (CAST(sqlc.arg(platform) AS TEXT) = '' OR vd.platform = sqlc.arg(platform))
  AND (CAST(sqlc.arg(channel) AS TEXT) = '' OR vd.channel_external_id = sqlc.arg(channel))
  AND (CAST(sqlc.arg(has_video) AS INTEGER) = 0
       OR (vd.platform = sqlc.arg(video_platform) AND vd.external_id = sqlc.arg(video_external_id)
           AND vd.part = sqlc.arg(video_part)))
  AND (CAST(sqlc.arg(lang) AS TEXT) = '' OR v.lang = sqlc.arg(lang))
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR v.kind = sqlc.arg(kind))
  AND (CAST(sqlc.arg(variant) AS TEXT) = '' OR v.variant = sqlc.arg(variant))
  AND (CAST(sqlc.arg(transcribe_model) AS TEXT) = '' OR v.transcribe_model = sqlc.arg(transcribe_model))
  AND (CAST(sqlc.arg(correct_model) AS TEXT) = '' OR v.correct_model = sqlc.arg(correct_model))
  AND (CAST(sqlc.arg(translate_model) AS TEXT) = '' OR v.translate_model = sqlc.arg(translate_model))
  AND (CAST(sqlc.arg(exclude_unverified) AS INTEGER) = 0 OR v.model_verified = 1)
  AND (CAST(sqlc.arg(see_all) AS INTEGER) = 1
       OR v.visibility IN (SELECT value FROM json_each(CAST(sqlc.arg(visibilities_json) AS TEXT)))
       OR (CAST(sqlc.arg(viewer_id) AS TEXT) <> '' AND EXISTS (
            SELECT 1 FROM share_versions sv JOIN shares s ON s.id = sv.share_id
            WHERE sv.version_id = v.id AND s.user_id = sqlc.arg(viewer_id) AND s.withdrawn_at IS NULL)))
  AND (CAST(sqlc.arg(has_cursor) AS INTEGER) = 0
       OR (v.score * CAST(sqlc.arg(by_score) AS INTEGER)) < sqlc.arg(cursor_score)
       OR ((v.score * CAST(sqlc.arg(by_score) AS INTEGER)) = sqlc.arg(cursor_score)
           AND (v.created_at < sqlc.arg(cursor_at)
                OR (v.created_at = sqlc.arg(cursor_at) AND v.id < CAST(sqlc.arg(cursor_id) AS TEXT)))))
ORDER BY sort_score DESC, v.created_at DESC, v.id DESC
LIMIT sqlc.arg(lim);

-- name: LookupVersions :many
-- Visible source or normalized transcripts of the videos whose key hash is in [lo, hi), for the pre-ASR lookup.
SELECT sqlc.embed(v), vd.key_sha256
FROM versions v JOIN videos vd ON vd.id = v.video_id
WHERE vd.platform = sqlc.arg(platform)
  AND vd.key_sha256 >= CAST(sqlc.arg(lo) AS TEXT) AND vd.key_sha256 < CAST(sqlc.arg(hi) AS TEXT)
  AND v.kind = 'transcript' AND v.variant IN ('source', 'normalized') AND v.state = 'visible'
  AND (CAST(sqlc.arg(see_all) AS INTEGER) = 1
       OR v.visibility IN (SELECT value FROM json_each(CAST(sqlc.arg(visibilities_json) AS TEXT)))
       OR (CAST(sqlc.arg(viewer_id) AS TEXT) <> '' AND EXISTS (
            SELECT 1 FROM share_versions sv JOIN shares s ON s.id = sv.share_id
            WHERE sv.version_id = v.id AND s.user_id = sqlc.arg(viewer_id) AND s.withdrawn_at IS NULL)))
ORDER BY vd.key_sha256, v.score DESC, v.created_at DESC, v.id DESC;
