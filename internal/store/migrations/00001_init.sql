-- +goose Up
-- 所有時間欄位都是 Unix 毫秒，取自注入的 clock。

-- settings 存實例層級的鍵值；目前只有 owner 密碼的 argon2id hash。
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- sessions 的 token 只存 hash；登出時直接刪除，立即失效（AUTH-R4）。
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    ip         TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX sessions_expires_at ON sessions (expires_at);

-- +goose Down
DROP INDEX sessions_expires_at;
DROP TABLE sessions;
DROP TABLE settings;
