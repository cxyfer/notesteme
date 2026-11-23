-- name: UpsertVote :exec
INSERT INTO votes (version_id, user_id, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (version_id, user_id) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;

-- name: DeleteVote :exec
DELETE FROM votes WHERE version_id = ? AND user_id = ?;

-- name: GetVote :one
SELECT * FROM votes WHERE version_id = ? AND user_id = ?;

-- name: AddVoteEvent :exec
INSERT INTO vote_events (user_id, at) VALUES (?, ?);

-- name: VoteUsage :one
SELECT COUNT(*) AS n, CAST(COALESCE(MIN(at), 0) AS INTEGER) AS oldest FROM vote_events WHERE user_id = ? AND at > ?;

-- name: PruneVoteEvents :exec
DELETE FROM vote_events WHERE user_id = ? AND at <= ?;

-- name: RecomputeScore :exec
-- The score only counts votes of accounts that are not suspended (HREP-R13).
UPDATE versions SET score = (
    SELECT CAST(COALESCE(SUM(v.value), 0) AS INTEGER) FROM votes v JOIN users u ON u.id = v.user_id
    WHERE v.version_id = versions.id AND u.suspended_at IS NULL
) WHERE versions.id = sqlc.arg(version_id);

-- name: RecomputeScoresOfVoter :exec
UPDATE versions SET score = (
    SELECT CAST(COALESCE(SUM(v.value), 0) AS INTEGER) FROM votes v JOIN users u ON u.id = v.user_id
    WHERE v.version_id = versions.id AND u.suspended_at IS NULL
) WHERE versions.id IN (SELECT vv.version_id FROM votes vv WHERE vv.user_id = sqlc.arg(user_id));

-- name: SetSuspended :execrows
UPDATE users SET suspended_at = ?, suspended_reason = ? WHERE id = ? AND suspended_at IS NULL;

-- name: ClearSuspended :execrows
UPDATE users SET suspended_at = NULL, suspended_reason = '' WHERE id = ? AND suspended_at IS NOT NULL;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: RevokeUserPATs :exec
UPDATE pats SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at, id LIMIT ?;

-- name: CreateReport :exec
INSERT INTO reports (id, version_id, reporter_id, reason, note, created_at) VALUES (?, ?, ?, ?, ?, ?);

-- name: OpenReportExists :one
SELECT COUNT(*) FROM reports WHERE version_id = ? AND reporter_id = ? AND state = 'open';

-- name: ReportUsage :one
SELECT COUNT(*) AS n, CAST(COALESCE(MIN(created_at), 0) AS INTEGER) AS oldest
FROM reports WHERE reporter_id = ? AND created_at > ?;

-- name: CountEstablishedReporters :one
-- Distinct reporters with an open report whose account was old enough when they reported and who are not suspended (HREP-R15).
SELECT COUNT(DISTINCT r.reporter_id) FROM reports r JOIN users u ON u.id = r.reporter_id
WHERE r.version_id = ? AND r.state = 'open' AND u.suspended_at IS NULL AND u.created_at <= r.created_at - sqlc.arg(min_age_ms);

-- name: SetVersionStateFrom :execrows
UPDATE versions SET state = ? WHERE id = ? AND state = ?;

-- name: GetReport :one
SELECT * FROM reports WHERE id = ?;

-- name: CloseOpenReports :exec
UPDATE reports SET state = ?, decided_at = ?, decided_by = ? WHERE version_id = ? AND state = 'open';

-- name: ListReports :many
SELECT * FROM reports WHERE (CAST(sqlc.arg(state) AS TEXT) = '' OR state = sqlc.arg(state))
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: MarkVersionRemoved :exec
UPDATE versions SET state = 'removed', removal_source = ?, removal_reason = ? WHERE id = ?;

-- name: LiveBlobRefs :one
-- Versions that are not removed and still reference the blob (withdrawn versions count: sharing again reuses them).
SELECT COUNT(DISTINCT vb.version_id) FROM version_blobs vb JOIN versions v ON v.id = vb.version_id
WHERE vb.sha256 = ? AND v.state <> 'removed';

-- name: VersionBlobSHAs :many
SELECT DISTINCT sha256 FROM version_blobs WHERE version_id = ? ORDER BY sha256;

-- name: CreateTakedown :exec
INSERT INTO takedowns (id, version_id, reason, statement, dek_nonce, dek_sealed, contact_nonce, contact_sealed, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTakedown :one
SELECT * FROM takedowns WHERE id = ?;

-- name: DecideTakedown :execrows
UPDATE takedowns SET state = ?, note = ?, decided_at = ?, decided_by = ? WHERE id = ? AND state = 'open';

-- name: ListTakedowns :many
SELECT * FROM takedowns WHERE (CAST(sqlc.arg(state) AS TEXT) = '' OR state = sqlc.arg(state))
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: UserShares :many
SELECT * FROM shares WHERE user_id = ? ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: DecideReport :execrows
-- Only an open report can be decided; the affected row count settles concurrent decisions.
UPDATE reports SET state = ?, decided_at = ?, decided_by = ? WHERE id = ? AND state = 'open';
