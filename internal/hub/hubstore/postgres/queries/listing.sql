-- name: ListVersions :many
-- Visible versions that match the filters and that the viewer may see, in keyset order
-- (score when by_score else 0, then created_at, then id, all descending). Empty filter values mean "any".
SELECT sqlc.embed(v), sqlc.embed(vd),
       CAST(CASE WHEN sqlc.arg(by_score)::boolean THEN v.score ELSE 0 END AS INTEGER) AS sort_score
FROM versions v JOIN videos vd ON vd.id = v.video_id
WHERE v.state = 'visible'
  AND (sqlc.arg(platform)::text = '' OR vd.platform = sqlc.arg(platform)::text)
  AND (sqlc.arg(channel)::text = '' OR vd.channel_external_id = sqlc.arg(channel)::text)
  AND (NOT sqlc.arg(has_video)::boolean
       OR (vd.platform = sqlc.arg(video_platform)::text AND vd.external_id = sqlc.arg(video_external_id)::text
           AND vd.part = sqlc.arg(video_part)::integer))
  AND (sqlc.arg(lang)::text = '' OR v.lang = sqlc.arg(lang)::text)
  AND (sqlc.arg(kind)::text = '' OR v.kind = sqlc.arg(kind)::text)
  AND (sqlc.arg(variant)::text = '' OR v.variant = sqlc.arg(variant)::text)
  AND (sqlc.arg(transcribe_model)::text = '' OR v.transcribe_model = sqlc.arg(transcribe_model)::text)
  AND (sqlc.arg(correct_model)::text = '' OR v.correct_model = sqlc.arg(correct_model)::text)
  AND (sqlc.arg(translate_model)::text = '' OR v.translate_model = sqlc.arg(translate_model)::text)
  AND (NOT sqlc.arg(exclude_unverified)::boolean OR v.model_verified)
  AND (sqlc.arg(see_all)::boolean
       OR v.visibility = ANY (sqlc.arg(visibilities)::text[])
       OR (sqlc.arg(viewer_id)::text <> '' AND EXISTS (
            SELECT 1 FROM share_versions sv JOIN shares s ON s.id = sv.share_id
            WHERE sv.version_id = v.id AND s.user_id = sqlc.arg(viewer_id)::text AND s.withdrawn_at IS NULL)))
  AND (NOT sqlc.arg(has_cursor)::boolean
       OR (CASE WHEN sqlc.arg(by_score)::boolean THEN v.score ELSE 0 END) < sqlc.arg(cursor_score)::integer
       OR ((CASE WHEN sqlc.arg(by_score)::boolean THEN v.score ELSE 0 END) = sqlc.arg(cursor_score)::integer
           AND (v.created_at < sqlc.arg(cursor_at)::timestamptz
                OR (v.created_at = sqlc.arg(cursor_at)::timestamptz AND v.id < sqlc.arg(cursor_id)::text))))
ORDER BY sort_score DESC, v.created_at DESC, v.id DESC
LIMIT sqlc.arg(lim)::integer;

-- name: LookupVersions :many
-- Visible source or normalized transcripts of the videos whose key hash is in [lo, hi), for the pre-ASR lookup.
-- key_sha256 is COLLATE "C", so the range is a byte-wise prefix match and can use the index.
SELECT sqlc.embed(v), vd.key_sha256
FROM versions v JOIN videos vd ON vd.id = v.video_id
WHERE vd.platform = sqlc.arg(platform)::text
  AND vd.key_sha256 >= sqlc.arg(lo)::text AND vd.key_sha256 < sqlc.arg(hi)::text
  AND v.kind = 'transcript' AND v.variant IN ('source', 'normalized') AND v.state = 'visible'
  AND (sqlc.arg(see_all)::boolean
       OR v.visibility = ANY (sqlc.arg(visibilities)::text[])
       OR (sqlc.arg(viewer_id)::text <> '' AND EXISTS (
            SELECT 1 FROM share_versions sv JOIN shares s ON s.id = sv.share_id
            WHERE sv.version_id = v.id AND s.user_id = sqlc.arg(viewer_id)::text AND s.withdrawn_at IS NULL)))
ORDER BY vd.key_sha256, v.score DESC, v.created_at DESC, v.id DESC;
