-- +goose Up
-- M5-S5：投票、檢舉、下架申請、停用與命令列的管理（HREP-R13～R15、HACC-R15～R17）。時間欄位是 Unix 毫秒，取自注入的 clock。

-- votes 是目前的票（一人一票，HREP-R13）；versions.score 在同一個交易中以「沒有被停用的帳號的票」重算。
CREATE TABLE votes (
    version_id TEXT NOT NULL REFERENCES versions (id),
    user_id    TEXT NOT NULL REFERENCES users (id),
    value      INTEGER NOT NULL CHECK (value IN (1, -1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (version_id, user_id)
) STRICT;

CREATE INDEX votes_user ON votes (user_id);

-- vote_events 記錄每一次投票與收回，用來計算每小時的次數（rate_limits.votes_per_hour）。
CREATE TABLE vote_events (
    user_id TEXT NOT NULL REFERENCES users (id),
    at      INTEGER NOT NULL
) STRICT;

CREATE INDEX vote_events_user ON vote_events (user_id, at);

-- reports 是檢舉（HREP-R14、R15）：state 為 open、dismissed（版主駁回）或 actioned（版本已移除）。
-- 同一人對同一版本最多一筆 open 的檢舉。
CREATE TABLE reports (
    id          TEXT PRIMARY KEY,
    version_id  TEXT NOT NULL REFERENCES versions (id),
    reporter_id TEXT NOT NULL REFERENCES users (id),
    reason      TEXT NOT NULL CHECK (reason IN ('wrong_language', 'bad_quality', 'spam', 'offensive', 'other')),
    note        TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'dismissed', 'actioned')),
    created_at  INTEGER NOT NULL,
    decided_at  INTEGER,
    decided_by  TEXT REFERENCES users (id)
) STRICT;

CREATE INDEX reports_version ON reports (version_id, state);
CREATE INDEX reports_reporter ON reports (reporter_id, created_at);
CREATE UNIQUE INDEX reports_open_once ON reports (version_id, reporter_id) WHERE state = 'open';

-- takedowns 是下架申請（HACC-R15、R16），不需要帳號。version_id 不檢查存在（不讓申請成為探測版本是否存在的管道）。
-- contact_email 以信封加密：每筆申請一把隨機的資料金鑰（dek_*，以主金鑰推導的 KEK 加密），email 以資料金鑰加密（contact_*）。
CREATE TABLE takedowns (
    id             TEXT PRIMARY KEY,
    version_id     TEXT NOT NULL,
    reason         TEXT NOT NULL CHECK (reason IN ('copyright', 'privacy', 'other')),
    statement      TEXT NOT NULL,
    dek_nonce      BLOB NOT NULL,
    dek_sealed     BLOB NOT NULL,
    contact_nonce  BLOB NOT NULL,
    contact_sealed BLOB NOT NULL,
    state          TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'removed', 'rejected')),
    note           TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    decided_at     INTEGER,
    decided_by     TEXT REFERENCES users (id)
) STRICT;

CREATE INDEX takedowns_created ON takedowns (created_at);

-- 版本被移除的原因（GET /api/v1/me/shares，HACC-R16）：source 為 takedown 或 report，reason 是申請或檢舉的理由。
ALTER TABLE versions ADD COLUMN removal_source TEXT NOT NULL DEFAULT '';
ALTER TABLE versions ADD COLUMN removal_reason TEXT NOT NULL DEFAULT '';

-- group_members 多一種來源 cli（notesteme-hub admin grant，不會被啟動時依設定檔的同步清掉，HACC A12）。SQLite 要重建表才能改 CHECK。
CREATE TABLE group_members_new (
    user_id       TEXT NOT NULL REFERENCES users (id),
    group_name    TEXT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('manual', 'rule', 'config', 'cli')),
    rule_provider TEXT NOT NULL DEFAULT '' CHECK ((source = 'rule') = (rule_provider <> '')),
    added_at      INTEGER NOT NULL,
    PRIMARY KEY (user_id, group_name, source, rule_provider)
) STRICT;
INSERT INTO group_members_new SELECT user_id, group_name, source, rule_provider, added_at FROM group_members;
DROP TABLE group_members;
ALTER TABLE group_members_new RENAME TO group_members;
CREATE INDEX group_members_group ON group_members (group_name, source);

-- audit_log 的 actor_user_id 可以是 NULL：命令列（notesteme-hub admin）的動作沒有登入的執行者。
CREATE TABLE audit_log_new (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT REFERENCES users (id),
    action        TEXT NOT NULL,
    target_kind   TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    detail_json   TEXT NOT NULL DEFAULT '{}',
    at            INTEGER NOT NULL
) STRICT;
INSERT INTO audit_log_new SELECT id, actor_user_id, action, target_kind, target_id, detail_json, at FROM audit_log;
DROP TABLE audit_log;
ALTER TABLE audit_log_new RENAME TO audit_log;
CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DELETE FROM audit_log WHERE actor_user_id IS NULL;
CREATE TABLE audit_log_old (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT NOT NULL REFERENCES users (id),
    action        TEXT NOT NULL,
    target_kind   TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    detail_json   TEXT NOT NULL DEFAULT '{}',
    at            INTEGER NOT NULL
) STRICT;
INSERT INTO audit_log_old SELECT * FROM audit_log;
DROP TABLE audit_log;
ALTER TABLE audit_log_old RENAME TO audit_log;
CREATE INDEX audit_log_at ON audit_log (at);
DELETE FROM group_members WHERE source = 'cli';
CREATE TABLE group_members_old (
    user_id       TEXT NOT NULL REFERENCES users (id),
    group_name    TEXT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('manual', 'rule', 'config')),
    rule_provider TEXT NOT NULL DEFAULT '' CHECK ((source = 'rule') = (rule_provider <> '')),
    added_at      INTEGER NOT NULL,
    PRIMARY KEY (user_id, group_name, source, rule_provider)
) STRICT;
INSERT INTO group_members_old SELECT * FROM group_members;
DROP TABLE group_members;
ALTER TABLE group_members_old RENAME TO group_members;
CREATE INDEX group_members_group ON group_members (group_name, source);
ALTER TABLE versions DROP COLUMN removal_reason;
ALTER TABLE versions DROP COLUMN removal_source;
DROP TABLE takedowns;
DROP TABLE reports;
DROP TABLE vote_events;
DROP TABLE votes;
