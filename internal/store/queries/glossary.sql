-- name: GetGlossary :one
SELECT * FROM glossaries WHERE scope = sqlc.arg(scope) AND scope_id = sqlc.arg(scope_id);

-- name: PutGlossary :exec
INSERT INTO glossaries (scope, scope_id, terms_json, updated_at)
VALUES (sqlc.arg(scope), sqlc.arg(scope_id), sqlc.arg(terms_json), sqlc.arg(now))
ON CONFLICT (scope, scope_id) DO UPDATE SET terms_json = excluded.terms_json, updated_at = excluded.updated_at;

-- name: InsertTermCandidate :execrows
-- A term already stored for the channel (pending, accepted or rejected) is not added again.
INSERT INTO glossary_candidates (id, channel_id, item_id, term, state, created_at)
VALUES (sqlc.arg(id), sqlc.arg(channel_id), sqlc.narg(item_id), sqlc.arg(term), 'pending', sqlc.arg(now))
ON CONFLICT (channel_id, term) DO NOTHING;

-- name: TermCandidatesByChannel :many
SELECT * FROM glossary_candidates WHERE channel_id = sqlc.arg(channel_id) ORDER BY created_at, rowid;

-- name: CountTermCandidates :one
SELECT COUNT(*) FROM glossary_candidates WHERE channel_id = sqlc.arg(channel_id);

-- name: GetTermCandidate :one
SELECT * FROM glossary_candidates WHERE id = sqlc.arg(id) AND channel_id = sqlc.arg(channel_id);

-- name: DecideTermCandidate :execrows
-- Only a pending candidate can be decided; a decided one keeps its state.
UPDATE glossary_candidates SET state = sqlc.arg(state)
WHERE id = sqlc.arg(id) AND channel_id = sqlc.arg(channel_id) AND state = 'pending';
