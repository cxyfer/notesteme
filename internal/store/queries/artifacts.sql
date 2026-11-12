-- name: SupersedeCurrentArtifacts :exec
-- Mark the current version of an (item, kind, variant, lang) group as superseded (PIPE-R7).
UPDATE artifacts SET superseded_by = CAST(sqlc.arg(superseded_by) AS TEXT)
WHERE item_id = sqlc.arg(item_id) AND kind = sqlc.arg(kind) AND variant = sqlc.arg(variant) AND lang = sqlc.arg(lang)
  AND superseded_by IS NULL;

-- name: InsertArtifact :exec
INSERT INTO artifacts (id, item_id, kind, variant, lang, format, path, sha256, size, parents_json, provenance_json, created_at)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(kind), sqlc.arg(variant), sqlc.arg(lang), sqlc.arg(format), sqlc.arg(path),
        sqlc.arg(sha256), sqlc.arg(size), sqlc.arg(parents_json), sqlc.arg(provenance_json), sqlc.arg(created_at));

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
INSERT INTO source_probes (id, item_id, run_id, source_id, lang, kind, available, est_cost, state, rejected_reason, probed_at)
VALUES (sqlc.arg(id), sqlc.arg(item_id), sqlc.arg(run_id), sqlc.arg(source_id), sqlc.arg(lang), sqlc.arg(kind),
        sqlc.arg(available), sqlc.narg(est_cost), sqlc.arg(state), sqlc.arg(rejected_reason), sqlc.arg(probed_at));

-- name: LatestProbes :many
-- Every result of the latest probe run of an item (run_id of the last written row), in insertion order.
SELECT * FROM source_probes AS sp
WHERE sp.item_id = sqlc.arg(item_id)
  AND sp.run_id = (SELECT p.run_id FROM source_probes AS p WHERE p.item_id = sqlc.arg(item_id) ORDER BY p.rowid DESC LIMIT 1)
ORDER BY sp.rowid;
