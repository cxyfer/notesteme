-- name: UpsertVote :exec
INSERT INTO votes (version_id, user_id, value, created_at, updated_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (version_id, user_id) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: DeleteVote :exec
DELETE FROM votes WHERE version_id = $1 AND user_id = $2;

-- name: AddVoteEvent :exec
INSERT INTO vote_events (user_id, at) VALUES ($1, $2);

-- name: VoteUsage :one
SELECT COUNT(*) AS n, CAST(COALESCE(MIN(at), 'epoch'::timestamptz) AS TIMESTAMPTZ) AS oldest FROM vote_events WHERE user_id = $1 AND at > $2;

-- name: PruneVoteEvents :exec
DELETE FROM vote_events WHERE user_id = $1 AND at <= $2;

-- name: RecomputeScore :one
-- The score only counts votes of accounts that are not suspended (HREP-R13).
UPDATE versions SET score = (
    SELECT CAST(COALESCE(SUM(v.value), 0) AS INTEGER) FROM votes v JOIN users u ON u.id = v.user_id
    WHERE v.version_id = versions.id AND u.suspended_at IS NULL
) WHERE versions.id = sqlc.arg(version_id)
RETURNING score;

-- name: VotedVersions :many
-- Versions the account voted on, in a fixed order so that concurrent recomputations lock rows in the same order.
SELECT version_id FROM votes WHERE user_id = $1 ORDER BY version_id;

-- name: SetSuspended :execrows
UPDATE users SET suspended_at = $1, suspended_reason = $2 WHERE id = $3 AND suspended_at IS NULL;

-- name: ClearSuspended :execrows
UPDATE users SET suspended_at = NULL, suspended_reason = '' WHERE id = $1 AND suspended_at IS NOT NULL;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherUserSessions :exec
-- Every session of the account except the one that made the request (removing a sign-in method).
DELETE FROM sessions WHERE user_id = $1 AND id <> $2;

-- name: RevokeUserPATs :exec
UPDATE pats SET revoked_at = $1 WHERE user_id = $2 AND revoked_at IS NULL;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at, id LIMIT $1;

-- name: CreateReport :exec
INSERT INTO reports (id, version_id, reporter_id, reason, note, created_at) VALUES ($1, $2, $3, $4, $5, $6);

-- name: OpenReportExists :one
SELECT COUNT(*) FROM reports WHERE version_id = $1 AND reporter_id = $2 AND state = 'open';

-- name: ReportUsage :one
SELECT COUNT(*) AS n, CAST(COALESCE(MIN(created_at), 'epoch'::timestamptz) AS TIMESTAMPTZ) AS oldest
FROM reports WHERE reporter_id = $1 AND created_at > $2;

-- name: CountEstablishedReporters :one
-- Distinct reporters with an open report whose account was old enough when they reported and who are not suspended (HREP-R15).
SELECT COUNT(DISTINCT r.reporter_id) FROM reports r JOIN users u ON u.id = r.reporter_id
WHERE r.version_id = sqlc.arg(version_id) AND r.state = 'open' AND u.suspended_at IS NULL
  AND u.created_at <= r.created_at - (sqlc.arg(min_age_ms)::bigint * INTERVAL '1 millisecond');

-- name: SetVersionStateFrom :execrows
UPDATE versions SET state = sqlc.arg(state) WHERE id = sqlc.arg(id) AND state = sqlc.arg(from_state);

-- name: GetReport :one
SELECT * FROM reports WHERE id = $1;

-- name: CloseOpenReports :exec
UPDATE reports SET state = $1, decided_at = $2, decided_by = $3 WHERE version_id = $4 AND state = 'open';

-- name: ListReports :many
SELECT * FROM reports WHERE (sqlc.arg(state)::text = '' OR state = sqlc.arg(state)::text)
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: MarkVersionRemoved :exec
UPDATE versions SET state = 'removed', removal_source = $1, removal_reason = $2 WHERE id = $3;

-- name: LiveBlobRefs :one
-- Versions that are not removed and still reference the blob (withdrawn versions count: sharing again reuses them).
SELECT COUNT(DISTINCT vb.version_id) FROM version_blobs vb JOIN versions v ON v.id = vb.version_id
WHERE vb.sha256 = $1 AND v.state <> 'removed';

-- name: VersionBlobSHAs :many
SELECT DISTINCT sha256 FROM version_blobs WHERE version_id = $1 ORDER BY sha256;

-- name: CreateTakedown :exec
INSERT INTO takedowns (id, version_id, reason, statement, dek_nonce, dek_sealed, contact_nonce, contact_sealed, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetTakedown :one
SELECT * FROM takedowns WHERE id = $1;

-- name: DecideTakedown :execrows
UPDATE takedowns SET state = $1, note = $2, decided_at = $3, decided_by = $4 WHERE id = $5 AND state = 'open';

-- name: ListTakedowns :many
SELECT * FROM takedowns WHERE (sqlc.arg(state)::text = '' OR state = sqlc.arg(state)::text)
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: UserShares :many
SELECT * FROM shares WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2;

-- name: DecideReport :execrows
-- Only an open report can be decided; the affected row count settles concurrent decisions.
UPDATE reports SET state = $1, decided_at = $2, decided_by = $3 WHERE id = $4 AND state = 'open';
