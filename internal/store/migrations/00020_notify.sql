-- +goose Up
-- Notifiers and the notification outbox (NOTIFY, ADR-0021 sections 5 and 7). Times are unix milliseconds.
--
-- notifiers.id is the owner's slug (NOTIFY-R1). The secret part of config (tokens, webhook urls and secrets) is sealed
-- with the data key (SEC-R4, AAD "notifier/<id>") into config_nonce + config_ciphertext; config_public_json holds the
-- non-secret fields and the last four characters, so listing never decrypts. events_json is a JSON array of event names.
CREATE TABLE notifiers (
    id                 TEXT    PRIMARY KEY,
    kind               TEXT    NOT NULL,
    config_nonce       BLOB    NOT NULL,
    config_ciphertext  BLOB    NOT NULL,
    config_public_json TEXT    NOT NULL DEFAULT '{}',
    events_json        TEXT    NOT NULL DEFAULT '[]',
    enabled            INTEGER NOT NULL DEFAULT 1,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
) STRICT;

-- One row per (notifier, event occurrence). payload_json never holds secrets (NOTIFY-R6, R9); state is
-- pending | sent | dropped. No foreign key on notifier_id on purpose: deleting a notifier removes its pending rows
-- (NOTIFY-R1) but keeps the sent and dropped history until the 30-day retention (NOTIFY-R8).
-- last_attempt_at drives the per-notifier rate limit (NOTIFY-R11); the implicit rowid keeps creation order when the
-- fake clock gives several rows the same created_at.
CREATE TABLE notification_outbox (
    id              TEXT    PRIMARY KEY,
    notifier_id     TEXT    NOT NULL,
    event           TEXT    NOT NULL,
    dedupe_key      TEXT    NOT NULL,
    payload_json    TEXT    NOT NULL DEFAULT '{}',
    state           TEXT    NOT NULL DEFAULT 'pending',
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL,
    last_attempt_at INTEGER,
    last_error      TEXT,
    created_at      INTEGER NOT NULL,
    sent_at         INTEGER
) STRICT;

CREATE INDEX notification_outbox_due ON notification_outbox (state, next_attempt_at);
CREATE INDEX notification_outbox_dedupe ON notification_outbox (notifier_id, dedupe_key, created_at);
CREATE INDEX notification_outbox_attempts ON notification_outbox (notifier_id, last_attempt_at);

-- +goose Down
DROP TABLE notification_outbox;
DROP TABLE notifiers;
