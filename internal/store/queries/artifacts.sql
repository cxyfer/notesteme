-- name: SupersedeCurrentArtifacts :exec
-- Mark the current version of an (item, kind, variant, lang) group as superseded (PIPE-R7).
UPDATE artifacts SET superseded_by = CAST(sqlc.arg(superseded_by) AS TEXT)
WHERE item_id = sqlc.arg(item_id) AND kind = sqlc.arg(kind) AND variant = sqlc.arg(variant) AND lang = sqlc.arg(lang)
  AND superseded_by IS NULL;

-- name: InsertArtifact :exec
INSERT INTO artifacts (id, item_id, kind, variant, lang, format, path, sha256, size, parents_json, provenance_json, timings_json, created_at)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(kind), sqlc.arg(variant), sqlc.arg(lang), sqlc.arg(format), sqlc.arg(path),
        sqlc.arg(sha256), sqlc.arg(size), sqlc.arg(parents_json), sqlc.arg(provenance_json), sqlc.arg(timings_json), sqlc.arg(created_at));

-- name: SupersedeOtherCurrentArtifacts :exec
-- Used when restoring an old artifact: current versions other than keep_id become superseded by keep_id.
UPDATE artifacts SET superseded_by = CAST(sqlc.arg(keep_id) AS TEXT)
WHERE item_id = sqlc.arg(item_id) AND kind = sqlc.arg(kind) AND variant = sqlc.arg(variant) AND lang = sqlc.arg(lang)
  AND superseded_by IS NULL AND id <> sqlc.arg(keep_id);

-- name: ClearArtifactSuperseded :exec
UPDATE artifacts SET superseded_by = NULL WHERE id = sqlc.arg(id);

-- name: ArtifactByID :one
SELECT * FROM artifacts WHERE id = sqlc.arg(id);

-- name: CurrentArtifacts :many
-- An empty kind means every kind.
SELECT * FROM artifacts
WHERE item_id = sqlc.arg(item_id) AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR kind = sqlc.arg(kind)) AND superseded_by IS NULL
ORDER BY created_at, rowid;

-- name: CountArtifactVersions :one
-- An empty lang means any language.
SELECT COUNT(*) FROM artifacts
WHERE item_id = sqlc.arg(item_id) AND kind = sqlc.arg(kind) AND variant = sqlc.arg(variant)
  AND (CAST(sqlc.arg(lang) AS TEXT) = '' OR lang = sqlc.arg(lang));

-- name: InsertProbe :exec
INSERT INTO source_probes (id, item_id, run_id, source_id, lang, kind, available, est_cost, state, rejected_reason, probed_at, probe_trigger)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(run_id), sqlc.arg(source_id), sqlc.arg(lang), sqlc.arg(kind),
        sqlc.arg(available), sqlc.narg(est_cost), sqlc.arg(state), sqlc.arg(rejected_reason), sqlc.arg(probed_at), sqlc.arg(probe_trigger));

-- name: LatestProbes :many
-- Every result of the latest stage probe run of an item (run_id of the last written row), in insertion order.
-- Reprobe results (SRC-R19) are kept apart: the detail shows the probe of the current transcript.
SELECT * FROM source_probes AS sp
WHERE sp.item_id = sqlc.arg(item_id)
  AND sp.run_id = (SELECT p.run_id FROM source_probes AS p
                   WHERE p.item_id = sqlc.arg(item_id) AND p.probe_trigger = 'stage' ORDER BY p.rowid DESC LIMIT 1)
ORDER BY sp.rowid;

-- name: ArtifactGroup :many
-- Every version of an (item, kind, variant, lang) group, newest first (creation order breaks ties).
SELECT * FROM artifacts
WHERE item_id = sqlc.arg(item_id) AND kind = sqlc.arg(kind) AND variant = sqlc.arg(variant) AND lang = sqlc.arg(lang)
ORDER BY created_at DESC, rowid DESC;

-- name: ArtifactParentRefs :many
-- Parent lists of every artifact of an item, so versions others were derived from are kept.
SELECT id, parents_json FROM artifacts WHERE item_id = sqlc.arg(item_id);

-- name: DetachStageRunsFromArtifact :exec
-- A purged artifact must not be referenced by stage runs (foreign key); they stay as history without a result.
UPDATE stage_runs SET artifact_id = NULL WHERE artifact_id = sqlc.arg(artifact_id);

-- name: DeleteArtifact :exec
DELETE FROM artifacts WHERE id = sqlc.arg(id);

-- name: RepointDanglingSuperseded :exec
-- After a purge, versions that were superseded by a deleted row are superseded by the current version instead.
UPDATE artifacts SET superseded_by = CAST(sqlc.arg(current_id) AS TEXT)
WHERE artifacts.item_id = sqlc.arg(item_id) AND artifacts.kind = sqlc.arg(kind) AND artifacts.variant = sqlc.arg(variant)
  AND artifacts.lang = sqlc.arg(lang) AND artifacts.superseded_by IS NOT NULL
  AND artifacts.superseded_by NOT IN (SELECT a.id FROM artifacts AS a);

-- name: PinArtifact :exec
-- A version a vault output references is kept when older versions are purged (ADR-0020 section 4).
UPDATE artifacts SET pinned = 1 WHERE id = sqlc.arg(id);

-- name: ItemArtifactSHAs :many
-- Content hashes of every version of every artifact of an item (bundle import skips identical content, BND-R9).
SELECT DISTINCT sha256 FROM artifacts WHERE item_id = sqlc.arg(item_id);

-- name: LatestBundleImport :one
-- The newest artifact of an item (any version) imported from a bundle (BND-R8): only the bundle shape of imported_from
-- (with bundle_sha256), not subtitle file imports, whose imported_from has a file name instead.
SELECT CAST(COALESCE(json_extract(provenance_json, '$.imported_from.instance_id'), '') AS TEXT) AS instance_id,
       CAST(COALESCE(json_extract(provenance_json, '$.imported_from.app_version'), '') AS TEXT) AS app_version,
       CAST(json_extract(provenance_json, '$.imported_from.bundle_sha256') AS TEXT) AS bundle_sha256,
       CAST(COALESCE(json_extract(provenance_json, '$.imported_from.imported_at'), '') AS TEXT) AS imported_at
FROM artifacts
WHERE item_id = sqlc.arg(item_id)
  AND json_type(provenance_json, '$.imported_from.bundle_sha256') = 'text'
ORDER BY created_at DESC, rowid DESC
LIMIT 1;

-- name: MarkArtifactCorrupt :execrows
-- Mark once: only the caller that changes the row (1) reruns the step that produced it, so concurrent readers that
-- find the same damaged file do not start several reruns.
UPDATE artifacts SET corrupt_at = CAST(sqlc.arg(now) AS INTEGER) WHERE id = sqlc.arg(id) AND corrupt_at IS NULL;
