-- +goose Up
-- M5-S5：投票、檢舉、下架申請、停用與命令列的管理，同 sqlite/migrations/00005_community.sql。
-- SQLite 要重建表才能改 CHECK 與 NOT NULL；Postgres 直接 ALTER（資料與 index 不動）。

CREATE TABLE votes (
    version_id TEXT COLLATE "C" NOT NULL REFERENCES versions (id),
    user_id    TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    value      INTEGER NOT NULL CHECK (value IN (1, -1)),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (version_id, user_id)
);

CREATE INDEX votes_user ON votes (user_id);

CREATE TABLE vote_events (
    user_id TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX vote_events_user ON vote_events (user_id, at);

CREATE TABLE reports (
    id          TEXT COLLATE "C" PRIMARY KEY,
    version_id  TEXT COLLATE "C" NOT NULL REFERENCES versions (id),
    reporter_id TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    reason      TEXT NOT NULL CHECK (reason IN ('wrong_language', 'bad_quality', 'spam', 'offensive', 'other')),
    note        TEXT NOT NULL DEFAULT '',
    state       TEXT COLLATE "C" NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'dismissed', 'actioned')),
    created_at  TIMESTAMPTZ NOT NULL,
    decided_at  TIMESTAMPTZ,
    decided_by  TEXT COLLATE "C" REFERENCES users (id)
);

CREATE INDEX reports_version ON reports (version_id, state);
CREATE INDEX reports_reporter ON reports (reporter_id, created_at);
CREATE UNIQUE INDEX reports_open_once ON reports (version_id, reporter_id) WHERE state = 'open';

CREATE TABLE takedowns (
    id             TEXT COLLATE "C" PRIMARY KEY,
    version_id     TEXT COLLATE "C" NOT NULL,
    reason         TEXT NOT NULL CHECK (reason IN ('copyright', 'privacy', 'other')),
    statement      TEXT NOT NULL,
    dek_nonce      BYTEA NOT NULL,
    dek_sealed     BYTEA NOT NULL,
    contact_nonce  BYTEA NOT NULL,
    contact_sealed BYTEA NOT NULL,
    state          TEXT COLLATE "C" NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'removed', 'rejected')),
    note           TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL,
    decided_at     TIMESTAMPTZ,
    decided_by     TEXT COLLATE "C" REFERENCES users (id)
);

CREATE INDEX takedowns_created ON takedowns (created_at);

ALTER TABLE versions ADD COLUMN removal_source TEXT NOT NULL DEFAULT '';
ALTER TABLE versions ADD COLUMN removal_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE group_members DROP CONSTRAINT group_members_source;
ALTER TABLE group_members ADD CONSTRAINT group_members_source CHECK (source IN ('manual', 'rule', 'config', 'cli'));

ALTER TABLE audit_log ALTER COLUMN actor_user_id DROP NOT NULL;

-- +goose Down
DELETE FROM audit_log WHERE actor_user_id IS NULL;
ALTER TABLE audit_log ALTER COLUMN actor_user_id SET NOT NULL;
DELETE FROM group_members WHERE source = 'cli';
ALTER TABLE group_members DROP CONSTRAINT group_members_source;
ALTER TABLE group_members ADD CONSTRAINT group_members_source CHECK (source IN ('manual', 'rule', 'config'));
ALTER TABLE versions DROP COLUMN removal_reason;
ALTER TABLE versions DROP COLUMN removal_source;
DROP TABLE takedowns;
DROP TABLE reports;
DROP TABLE vote_events;
DROP TABLE votes;
