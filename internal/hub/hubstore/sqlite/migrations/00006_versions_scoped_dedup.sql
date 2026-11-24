-- +goose NO TRANSACTION
-- +goose Up
-- 安全修補 A：版本的鍵 (video_id, kind, variant, lang, sha256) 不再唯一（HREP 待決問題 R8「去重與可見度」）。
-- 新上傳者看不到既有版本（private、別人的 group、hidden）或可見度不同時另建一列、只共用 blob。
-- SQLite 不能移除表格層級的 UNIQUE，只能重建表格：依官方步驟關閉外鍵、在交易中複製、檢查外鍵後再打開。
-- 欄位順序必須與原表相同（sqlc 的 SELECT * 與 sqlc.embed 依賴它）；既有資料全部保留。
PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE versions_new (
    id               TEXT PRIMARY KEY,
    video_id         TEXT NOT NULL REFERENCES videos (id),
    kind             TEXT NOT NULL CHECK (kind IN ('transcript', 'note', 'chapters', 'frames')),
    variant          TEXT NOT NULL DEFAULT '',
    lang             TEXT NOT NULL DEFAULT '',
    format           TEXT NOT NULL,
    sha256           TEXT NOT NULL REFERENCES blobs (sha256),
    size             INTEGER NOT NULL,
    provenance_json  TEXT NOT NULL DEFAULT '{}',
    transcribe_model TEXT NOT NULL DEFAULT '',
    correct_model    TEXT NOT NULL DEFAULT '',
    translate_model  TEXT NOT NULL DEFAULT '',
    model_verified   INTEGER NOT NULL,
    visibility       TEXT NOT NULL,
    license          TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'visible' CHECK (state IN ('visible', 'hidden', 'removed', 'withdrawn')),
    score            INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    removal_source   TEXT NOT NULL DEFAULT '',
    removal_reason   TEXT NOT NULL DEFAULT ''
) STRICT;

INSERT INTO versions_new (id, video_id, kind, variant, lang, format, sha256, size, provenance_json, transcribe_model,
                          correct_model, translate_model, model_verified, visibility, license, state, score, created_at,
                          removal_source, removal_reason)
SELECT id, video_id, kind, variant, lang, format, sha256, size, provenance_json, transcribe_model,
       correct_model, translate_model, model_verified, visibility, license, state, score, created_at,
       removal_source, removal_reason
FROM versions;

DROP TABLE versions;

ALTER TABLE versions_new RENAME TO versions;

CREATE INDEX versions_sha ON versions (sha256);
CREATE INDEX versions_key ON versions (video_id, kind, variant, lang, sha256);

COMMIT;

PRAGMA foreign_keys = ON;

-- +goose Down
-- 回復唯一鍵：同一個鍵已有多列時失敗（不能自動合併不同上傳者的版本），需要人工處理。
PRAGMA foreign_keys = OFF;

BEGIN;

CREATE TABLE versions_old (
    id               TEXT PRIMARY KEY,
    video_id         TEXT NOT NULL REFERENCES videos (id),
    kind             TEXT NOT NULL CHECK (kind IN ('transcript', 'note', 'chapters', 'frames')),
    variant          TEXT NOT NULL DEFAULT '',
    lang             TEXT NOT NULL DEFAULT '',
    format           TEXT NOT NULL,
    sha256           TEXT NOT NULL REFERENCES blobs (sha256),
    size             INTEGER NOT NULL,
    provenance_json  TEXT NOT NULL DEFAULT '{}',
    transcribe_model TEXT NOT NULL DEFAULT '',
    correct_model    TEXT NOT NULL DEFAULT '',
    translate_model  TEXT NOT NULL DEFAULT '',
    model_verified   INTEGER NOT NULL,
    visibility       TEXT NOT NULL,
    license          TEXT NOT NULL,
    state            TEXT NOT NULL DEFAULT 'visible' CHECK (state IN ('visible', 'hidden', 'removed', 'withdrawn')),
    score            INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    removal_source   TEXT NOT NULL DEFAULT '',
    removal_reason   TEXT NOT NULL DEFAULT '',
    UNIQUE (video_id, kind, variant, lang, sha256)
) STRICT;

INSERT INTO versions_old SELECT * FROM versions;

DROP TABLE versions;

ALTER TABLE versions_old RENAME TO versions;

CREATE INDEX versions_sha ON versions (sha256);

COMMIT;

PRAGMA foreign_keys = ON;
