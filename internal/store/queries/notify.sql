-- Notifiers and the notification outbox (NOTIFY). events_json is a JSON array; json_each is used instead of
-- sqlc.slice (see tasks.sql).

-- name: InsertNotifier :execrows
INSERT INTO notifiers (id, kind, config_nonce, config_ciphertext, config_public_json, events_json, enabled, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(config_nonce), sqlc.arg(config_ciphertext), sqlc.arg(config_public_json),
        sqlc.arg(events_json), sqlc.arg(enabled), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (id) DO NOTHING;

-- name: UpdateNotifier :execrows
UPDATE notifiers
SET config_nonce = sqlc.arg(config_nonce), config_ciphertext = sqlc.arg(config_ciphertext),
    config_public_json = sqlc.arg(config_public_json), events_json = sqlc.arg(events_json),
    enabled = sqlc.arg(enabled), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id);

-- name: GetNotifier :one
SELECT * FROM notifiers WHERE id = sqlc.arg(id);

-- name: ListNotifiers :many
SELECT * FROM notifiers ORDER BY created_at, id;

-- name: CountNotifiers :one
SELECT COUNT(*) FROM notifiers;

-- name: DeleteNotifier :execrows
DELETE FROM notifiers WHERE id = sqlc.arg(id);

-- name: NotifierIDsForEvent :many
-- Enabled notifiers that subscribed to the event, in creation order. events_json is a JSON array of validated names
-- (no quotes or escapes), so looking for the quoted name is exact.
SELECT id FROM notifiers
WHERE enabled = 1
  AND instr(events_json, json_quote(CAST(sqlc.arg(event) AS TEXT))) > 0
ORDER BY created_at, id;

-- name: OutboxDedupeExists :one
-- Any state counts (NOTIFY-R10): a key seen within the window is not written again.
SELECT CAST(EXISTS (
    SELECT 1 FROM notification_outbox
    WHERE notifier_id = sqlc.arg(notifier_id) AND dedupe_key = sqlc.arg(dedupe_key) AND created_at > sqlc.arg(since)
) AS INTEGER);

-- name: InsertOutbox :exec
INSERT INTO notification_outbox (id, notifier_id, event, dedupe_key, payload_json, state, attempts, next_attempt_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(notifier_id), sqlc.arg(event), sqlc.arg(dedupe_key), sqlc.arg(payload_json), 'pending', 0,
        sqlc.arg(now), sqlc.arg(now));

-- name: DueOutbox :many
-- Pending rows whose next attempt is due, oldest first.
SELECT * FROM notification_outbox
WHERE state = 'pending' AND next_attempt_at <= sqlc.arg(now)
ORDER BY next_attempt_at, rowid
LIMIT sqlc.arg(max);

-- name: NextOutboxAttempt :one
-- pending is 0 when nothing is pending (next_attempt_at is then 0, not NULL).
SELECT CAST(COUNT(*) AS INTEGER) AS pending, CAST(COALESCE(MIN(next_attempt_at), 0) AS INTEGER) AS next_attempt_at
FROM notification_outbox WHERE state = 'pending';

-- name: OutboxAttemptsSince :many
-- Attempt times of one notifier after since (exclusive), oldest first; the rate limit counts them (NOTIFY-R11).
SELECT CAST(last_attempt_at AS INTEGER) AS last_attempt_at FROM notification_outbox
WHERE notifier_id = sqlc.arg(notifier_id) AND last_attempt_at > CAST(sqlc.arg(since) AS INTEGER)
ORDER BY last_attempt_at;

-- name: RecordOutboxAttempt :execrows
-- Store the outcome of one attempt (the caller computes every field). Only pending rows change, so a row deleted or
-- finished meanwhile is left alone.
UPDATE notification_outbox
SET state = sqlc.arg(state), attempts = sqlc.arg(attempts), next_attempt_at = sqlc.arg(next_attempt_at),
    last_attempt_at = sqlc.arg(last_attempt_at), last_error = sqlc.narg(last_error), sent_at = sqlc.narg(sent_at)
WHERE id = sqlc.arg(id) AND state = 'pending';

-- name: DeferOutbox :execrows
-- Move a rate-limited row to a later slot without counting an attempt (NOTIFY-R11).
UPDATE notification_outbox SET next_attempt_at = sqlc.arg(next_attempt_at)
WHERE id = sqlc.arg(id) AND state = 'pending';

-- name: ListOutbox :many
-- Latest rows first; state is '' for every state.
SELECT * FROM notification_outbox
WHERE CAST(sqlc.arg(state) AS TEXT) = '' OR state = CAST(sqlc.arg(state) AS TEXT)
ORDER BY created_at DESC, rowid DESC
LIMIT sqlc.arg(max);

-- name: DeletePendingOutbox :execrows
DELETE FROM notification_outbox WHERE notifier_id = sqlc.arg(notifier_id) AND state = 'pending';

-- name: DeleteFinishedOutboxBefore :execrows
-- Sent and dropped rows are kept for 30 days (NOTIFY-R8).
DELETE FROM notification_outbox WHERE state IN ('sent', 'dropped') AND created_at < sqlc.arg(cutoff);
