-- +goose Up
-- Subscriptions (SUB, ADR-0021 section 7). Times are unix milliseconds. The next check lives only in
-- scheduled_jobs (kind subscription.check, key = subscriptions.id); this table keeps the outcome of the last check.
-- (kind, platform, external_id) is not a unique index: a handle subscription that turns out to be an existing
-- channel keeps the resolved channel id and is disabled (SUB-R12), so the duplicate check is done by the service
-- inside the single write transaction. baseline_at is set by the first successful check (SUB-R3).
CREATE TABLE subscriptions (
    id                TEXT    NOT NULL PRIMARY KEY,
    kind              TEXT    NOT NULL,
    platform          TEXT    NOT NULL,
    external_id       TEXT    NOT NULL,
    url               TEXT    NOT NULL,
    title             TEXT    NOT NULL DEFAULT '',
    profile_id        TEXT,
    overrides_json    TEXT    NOT NULL DEFAULT '{}',
    schedule_s        INTEGER NOT NULL,
    backfill_json     TEXT    NOT NULL,
    filters_json      TEXT    NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        INTEGER NOT NULL,
    baseline_at       INTEGER,
    last_checked_at   INTEGER,
    last_error        TEXT,
    last_error_class  TEXT,
    reevaluate        INTEGER NOT NULL DEFAULT 0
) STRICT;

CREATE INDEX subscriptions_source ON subscriptions (kind, platform, external_id);

-- One row per entry a check has seen (SUB-R2, SUB-R5). decision is created, skipped or deferred.
CREATE TABLE subscription_entries (
    subscription_id TEXT    NOT NULL REFERENCES subscriptions (id) ON DELETE CASCADE,
    platform        TEXT    NOT NULL,
    external_id     TEXT    NOT NULL,
    part            INTEGER NOT NULL DEFAULT 0,
    title           TEXT    NOT NULL DEFAULT '',
    published_at    INTEGER,
    duration_s      INTEGER,
    live_status     TEXT,
    position        INTEGER NOT NULL DEFAULT 0,
    decision        TEXT    NOT NULL,
    reason          TEXT    NOT NULL DEFAULT '',
    item_id         TEXT,
    seen_at         INTEGER NOT NULL,
    PRIMARY KEY (subscription_id, platform, external_id, part)
) STRICT;

ALTER TABLE items ADD COLUMN subscription_id TEXT;

-- +goose Down
ALTER TABLE items DROP COLUMN subscription_id;
DROP TABLE subscription_entries;
DROP TABLE subscriptions;
