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
-- Only a pending candidate can be decided; a decided one keeps its state and its first decision (GLOS-R1).
UPDATE glossary_candidates
SET state = sqlc.arg(state), decided_term = sqlc.narg(decided_term), decided_scope = sqlc.narg(decided_scope),
    decided_at = sqlc.arg(now)
WHERE id = sqlc.arg(id) AND channel_id = sqlc.arg(channel_id) AND state = 'pending';

-- name: GetTermCandidateByID :one
SELECT * FROM glossary_candidates WHERE id = sqlc.arg(id);

-- name: InsertTermCandidateAt :execrows
-- Same as InsertTermCandidate with an explicit created_at (test seeding, GLOS steps).
INSERT INTO glossary_candidates (id, channel_id, item_id, term, state, created_at)
VALUES (sqlc.arg(id), sqlc.arg(channel_id), sqlc.narg(item_id), sqlc.arg(term), 'pending', sqlc.arg(created_at))
ON CONFLICT (channel_id, term) DO NOTHING;

-- name: ReviewTermCandidates :many
-- The review list across channels (GLOS-R5): oldest first, then by id; after_ms/after_id is the cursor position.
SELECT c.id, c.channel_id, c.item_id, c.term, c.state, c.created_at, c.decided_term, c.decided_scope, c.decided_at,
       ch.platform AS channel_platform, ch.external_id AS channel_external_id, ch.title AS channel_title,
       i.platform AS item_platform, i.external_id AS item_external_id, i.part AS item_part, i.title AS item_title
FROM glossary_candidates c
JOIN channels ch ON ch.id = c.channel_id
LEFT JOIN items i ON i.id = c.item_id
WHERE (CAST(sqlc.arg(state) AS TEXT) = 'all' OR c.state = sqlc.arg(state))
  AND (CAST(sqlc.arg(channel_id) AS TEXT) = '' OR c.channel_id = sqlc.arg(channel_id))
  AND (CAST(sqlc.arg(after_ms) AS INTEGER) < 0 OR c.created_at > sqlc.arg(after_ms) OR (c.created_at = sqlc.arg(after_ms) AND c.id > sqlc.arg(after_id)))
ORDER BY c.created_at, c.id
LIMIT sqlc.arg(lim);

-- name: CountReviewTermCandidates :one
SELECT COUNT(*) FROM glossary_candidates c
WHERE (CAST(sqlc.arg(state) AS TEXT) = 'all' OR c.state = sqlc.arg(state))
  AND (CAST(sqlc.arg(channel_id) AS TEXT) = '' OR c.channel_id = sqlc.arg(channel_id));
