-- name: VideoByKey :one
SELECT * FROM videos WHERE platform = ? AND external_id = ? AND part = ?;

-- name: VideoByID :one
SELECT * FROM videos WHERE id = ?;

-- name: CreateVideo :exec
INSERT INTO videos (id, platform, external_id, part, title, channel_external_id, channel_title, duration_s,
                    original_lang, published_at, key_sha256, first_shared_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: IsRemovedHash :one
SELECT COUNT(*) FROM removed_hashes WHERE video_id = ? AND sha256 = ?;

-- name: AddRemovedHash :exec
INSERT INTO removed_hashes (video_id, sha256, takedown_id, removed_at) VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: AddBlob :exec
-- Blobs are keyed by content; the first record wins.
INSERT INTO blobs (sha256, size, media_type, created_at) VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING;

-- name: VersionsByKey :many
-- Several rows may share a key: each visibility scope keeps its own row (security fix A).
SELECT * FROM versions WHERE video_id = ? AND kind = ? AND variant = ? AND lang = ? AND sha256 = ?
ORDER BY created_at, id;

-- name: UserSharesVersion :one
SELECT COUNT(*) FROM share_versions sv JOIN shares s ON s.id = sv.share_id
WHERE sv.version_id = ? AND s.user_id = ? AND s.withdrawn_at IS NULL;

-- name: RestoreVersion :execrows
-- A withdrawn version shared again takes the values of the new share.
UPDATE versions SET state = 'visible', format = ?, size = ?, provenance_json = ?, transcribe_model = ?, correct_model = ?,
                    translate_model = ?, model_verified = ?, visibility = ?, license = ?, removal_source = '', removal_reason = ''
WHERE id = ? AND state = 'withdrawn';

-- name: DeleteChildDerivations :exec
-- A restored version takes the new share's provenance, so its own parents are rebuilt from it.
DELETE FROM derivations WHERE child_version_id = ?;

-- name: DeleteVersionImageBlobs :exec
DELETE FROM version_blobs WHERE version_id = ? AND role = 'image';

-- name: VersionByID :one
SELECT * FROM versions WHERE id = ?;

-- name: CreateVersion :exec
INSERT INTO versions (id, video_id, kind, variant, lang, format, sha256, size, provenance_json, transcribe_model,
                      correct_model, translate_model, model_verified, visibility, license, state, score, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddVersionBlob :exec
INSERT INTO version_blobs (version_id, sha256, role) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;

-- name: DeleteVersionContentBlobs :exec
DELETE FROM version_blobs WHERE version_id = ? AND role = 'content';

-- name: SetVersionContent :execrows
UPDATE versions SET sha256 = ?, size = ? WHERE id = ?;

-- name: VideoVersions :many
SELECT * FROM versions WHERE video_id = ? ORDER BY created_at, id;

-- name: VersionsBySHA :many
SELECT * FROM versions WHERE video_id = ? AND sha256 = ? ORDER BY created_at, id;

-- name: SetVersionState :execrows
UPDATE versions SET state = ? WHERE id = ?;

-- name: ActiveShareByBundle :one
-- The latest share of the same bundle by the same account that is not withdrawn (idempotent re-share).
SELECT * FROM shares WHERE user_id = ? AND bundle_sha256 = ? AND withdrawn_at IS NULL
ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: CreateShare :exec
INSERT INTO shares (id, user_id, video_id, license, attested_at, bundle_sha256, bundle_size, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddShareVersion :exec
INSERT INTO share_versions (share_id, version_id, position, deduplicated) VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;

-- name: ShareVersions :many
SELECT sqlc.embed(v), sv.deduplicated FROM share_versions sv JOIN versions v ON v.id = sv.version_id
WHERE sv.share_id = ? ORDER BY sv.position;

-- name: ShareByID :one
SELECT * FROM shares WHERE id = ?;

-- name: WithdrawShare :execrows
UPDATE shares SET withdrawn_at = ? WHERE id = ? AND user_id = ? AND withdrawn_at IS NULL;

-- name: ActiveSharesOfVersion :one
SELECT COUNT(*) FROM share_versions sv JOIN shares s ON s.id = sv.share_id
WHERE sv.version_id = ? AND s.withdrawn_at IS NULL;

-- name: ShareUsage :one
SELECT COUNT(*) AS n, CAST(COALESCE(SUM(bundle_size), 0) AS INTEGER) AS bytes,
       CAST(COALESCE(MIN(created_at), 0) AS INTEGER) AS oldest
FROM shares WHERE user_id = ? AND created_at > ?;

-- name: VersionUploaders :many
-- Accounts with a share that is not withdrawn, in the order of their first share.
SELECT u.id, u.display_name, CAST(MIN(s.created_at) AS INTEGER) AS first_at
FROM share_versions sv JOIN shares s ON s.id = sv.share_id JOIN users u ON u.id = s.user_id
WHERE sv.version_id = ? AND s.withdrawn_at IS NULL
GROUP BY u.id, u.display_name ORDER BY first_at, u.id;

-- name: AddDerivation :exec
INSERT INTO derivations (child_version_id, parent_version_id, relation) VALUES (?, ?, ?) ON CONFLICT DO NOTHING;

-- name: VersionParents :many
SELECT sqlc.embed(v), d.relation FROM derivations d JOIN versions v ON v.id = d.parent_version_id
WHERE d.child_version_id = ? ORDER BY v.created_at, v.id, d.relation;

-- name: VersionChildren :many
SELECT sqlc.embed(v), d.relation FROM derivations d JOIN versions v ON v.id = d.child_version_id
WHERE d.parent_version_id = ? ORDER BY v.created_at, v.id, d.relation;

-- name: BlobRefs :one
SELECT COUNT(DISTINCT version_id) FROM version_blobs WHERE sha256 = ?;
