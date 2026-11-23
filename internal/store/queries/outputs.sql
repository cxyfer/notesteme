-- name: UpsertOutput :exec
-- One row per (item, path): a rewrite replaces the recorded hash with the one just written.
-- kind, vault and git_commit describe vault outputs (ADR-0020 section 8); subtitle targets pass 'subtitle' and NULLs.
INSERT INTO outputs (item_id, path, target, format, lang, sha256, size, written_at, kind, vault, git_commit, state)
VALUES (sqlc.arg(item_id), sqlc.arg(path), sqlc.arg(target), sqlc.arg(format), sqlc.arg(lang),
        sqlc.arg(sha256), sqlc.arg(size), sqlc.arg(written_at), sqlc.arg(kind), sqlc.narg(vault), sqlc.narg(git_commit),
        sqlc.arg(state))
ON CONFLICT (item_id, path) DO UPDATE SET
    target = excluded.target, format = excluded.format, lang = excluded.lang,
    sha256 = excluded.sha256, size = excluded.size, written_at = excluded.written_at,
    kind = excluded.kind, vault = excluded.vault, git_commit = excluded.git_commit, state = excluded.state;

-- name: GetOutput :one
SELECT * FROM outputs WHERE item_id = sqlc.arg(item_id) AND path = sqlc.arg(path);

-- name: ListOutputs :many
SELECT * FROM outputs WHERE item_id = sqlc.arg(item_id) ORDER BY written_at, path;

-- name: SetOutputState :exec
-- Updates only the publish state of a recorded file (VAULT-R5): the hash keeps telling whether the user changed it.
UPDATE outputs SET state = sqlc.arg(state) WHERE item_id = sqlc.arg(item_id) AND path = sqlc.arg(path);

-- name: SetOutputCommit :exec
-- Records the git commit that a vault file landed in (VAULT-R7).
UPDATE outputs SET git_commit = sqlc.narg(git_commit) WHERE item_id = sqlc.arg(item_id) AND path = sqlc.arg(path);
