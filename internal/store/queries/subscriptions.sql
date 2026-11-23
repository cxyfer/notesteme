-- Subscriptions (SUB). The next check is in scheduled_jobs (kind subscription.check).

-- name: InsertSubscription :exec
INSERT INTO subscriptions (id, kind, platform, external_id, url, title, profile_id, overrides_json, schedule_s,
                           backfill_json, filters_json, enabled, created_at)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(platform), sqlc.arg(external_id), sqlc.arg(url), sqlc.arg(title),
        sqlc.narg(profile_id), sqlc.arg(overrides_json), sqlc.arg(schedule_s), sqlc.arg(backfill_json),
        sqlc.arg(filters_json), sqlc.arg(enabled), sqlc.arg(created_at));

-- name: GetSubscription :one
SELECT * FROM subscriptions WHERE id = sqlc.arg(id);

-- name: ListSubscriptions :many
-- In creation order; rowid breaks ties of the same created_at (SUB-R9).
SELECT * FROM subscriptions ORDER BY created_at, rowid;

-- name: CountSubscriptions :one
SELECT CAST(COUNT(*) AS INTEGER) FROM subscriptions;

-- name: SubscriptionsBySource :many
SELECT * FROM subscriptions
WHERE kind = sqlc.arg(kind) AND platform = sqlc.arg(platform) AND external_id = sqlc.arg(external_id)
ORDER BY created_at, rowid;

-- name: UpdateSubscriptionSettings :execrows
UPDATE subscriptions
SET profile_id = sqlc.narg(profile_id), overrides_json = sqlc.arg(overrides_json), schedule_s = sqlc.arg(schedule_s),
    backfill_json = sqlc.arg(backfill_json), filters_json = sqlc.arg(filters_json), enabled = sqlc.arg(enabled),
    reevaluate = sqlc.arg(reevaluate)
WHERE id = sqlc.arg(id);

-- name: RecordSubscriptionCheck :execrows
-- Outcome of one check; baseline_at keeps its first value.
UPDATE subscriptions
SET title = sqlc.arg(title), external_id = sqlc.arg(external_id), enabled = sqlc.arg(enabled),
    baseline_at = COALESCE(baseline_at, sqlc.narg(baseline_at)), last_checked_at = sqlc.arg(last_checked_at),
    last_error = sqlc.narg(last_error), last_error_class = sqlc.narg(last_error_class), reevaluate = sqlc.arg(reevaluate)
WHERE id = sqlc.arg(id);

-- name: DeleteSubscription :execrows
DELETE FROM subscriptions WHERE id = sqlc.arg(id);

-- name: ClearItemsSubscription :exec
UPDATE items SET subscription_id = NULL WHERE subscription_id = sqlc.arg(subscription_id);

-- name: SetItemSubscription :exec
UPDATE items SET subscription_id = sqlc.arg(subscription_id) WHERE id = sqlc.arg(id);

-- name: UpsertSubscriptionEntry :exec
INSERT INTO subscription_entries (subscription_id, platform, external_id, part, title, published_at, duration_s,
                                  live_status, position, decision, reason, item_id, seen_at)
VALUES (sqlc.arg(subscription_id), sqlc.arg(platform), sqlc.arg(external_id), sqlc.arg(part), sqlc.arg(title),
        sqlc.narg(published_at), sqlc.narg(duration_s), sqlc.narg(live_status), sqlc.arg(position),
        sqlc.arg(decision), sqlc.arg(reason), sqlc.narg(item_id), sqlc.arg(seen_at))
ON CONFLICT (subscription_id, platform, external_id, part) DO UPDATE SET
    title = excluded.title, published_at = COALESCE(excluded.published_at, published_at),
    duration_s = COALESCE(excluded.duration_s, duration_s), live_status = COALESCE(excluded.live_status, live_status),
    decision = excluded.decision, reason = excluded.reason, item_id = excluded.item_id;

-- name: ListSubscriptionEntries :many
-- Newest first: by seen_at, then publication time, then list position.
SELECT * FROM subscription_entries
WHERE subscription_id = sqlc.arg(subscription_id)
ORDER BY seen_at DESC, COALESCE(published_at, 0) DESC, position
LIMIT sqlc.arg(max);

-- name: CountSubscriptionEntries :one
SELECT CAST(COUNT(*) AS INTEGER) FROM subscription_entries WHERE subscription_id = sqlc.arg(subscription_id);

-- name: TombstoneExists :one
SELECT CAST(COUNT(*) AS INTEGER) FROM item_tombstones
WHERE platform = sqlc.arg(platform) AND external_id = sqlc.arg(external_id) AND part = sqlc.arg(part);
