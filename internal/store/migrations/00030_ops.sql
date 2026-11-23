-- +goose Up
-- OPS-R12～R18 (M4): unattended operation.
-- alert_states keeps one row per alert condition (kind, key). A row is active while resolved_at is NULL;
-- message is the latest text shown by GET /api/system (the spec lists the other columns).
CREATE TABLE alert_states (
    kind         TEXT    NOT NULL,
    key          TEXT    NOT NULL,
    message      TEXT    NOT NULL DEFAULT '',
    active_since INTEGER NOT NULL,
    notified_at  INTEGER,
    resolved_at  INTEGER,
    PRIMARY KEY (kind, key)
) STRICT;

-- tool_call_stats counts external tool calls in 5-minute buckets (OPS-R18), kept for 48 hours.
CREATE TABLE tool_call_stats (
    tool         TEXT    NOT NULL,
    bucket_start INTEGER NOT NULL,
    calls        INTEGER NOT NULL DEFAULT 0,
    failures     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (tool, bucket_start)
) STRICT;

-- last_ok_at: the last yt-dlp call that used this cookie and exited with 0 (OPS-R17); NULL until then.
ALTER TABLE secrets ADD COLUMN last_ok_at INTEGER;

-- +goose Down
ALTER TABLE secrets DROP COLUMN last_ok_at;
DROP TABLE tool_call_stats;
DROP TABLE alert_states;
