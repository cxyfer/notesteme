-- name: GetReprobe :one
SELECT * FROM source_reprobes WHERE item_id = sqlc.arg(item_id);

-- name: UpsertReprobe :exec
INSERT INTO source_reprobes (item_id, until_at, runs, sources, sponsorblock, better_json, auto_reruns, updated_at)
VALUES (sqlc.arg(item_id), sqlc.arg(until_at), sqlc.arg(runs), sqlc.arg(sources), sqlc.arg(sponsorblock),
        sqlc.narg(better_json), sqlc.arg(auto_reruns), sqlc.arg(updated_at))
ON CONFLICT (item_id) DO UPDATE SET
    until_at = excluded.until_at, runs = excluded.runs, sources = excluded.sources,
    sponsorblock = excluded.sponsorblock, better_json = excluded.better_json,
    auto_reruns = excluded.auto_reruns, updated_at = excluded.updated_at;

-- name: SetStageMessage :execrows
-- Changes only the message and actions of a stage run (SRC-R21, SRC-R20): the status and its times stay.
UPDATE stage_runs SET error_message = sqlc.arg(message), actions_json = sqlc.arg(actions_json)
WHERE id = sqlc.arg(id);
