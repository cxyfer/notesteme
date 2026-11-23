-- +goose Up
-- Hub 的 schema（ADR-0031 第 5 點）。所有時間欄位都是 Unix 毫秒，取自注入的 clock；ID 是 UUIDv7。
-- M5-S0 只建帳號與身分（第一個實作切片 HACC 登入與 notesteme-hub admin 都從這裡開始），其他表由各切片加 migration。

-- users 是 Hub 帳號；停用時 suspended_at 有值（HACC-R17）。
CREATE TABLE users (
    id               TEXT PRIMARY KEY,
    display_name     TEXT NOT NULL,
    created_at       INTEGER NOT NULL,
    suspended_at     INTEGER,
    suspended_reason TEXT NOT NULL DEFAULT ''
) STRICT;

-- identities 是登入方式：以 (provider, subject) 識別，login 與 email 只用來顯示與套用規則（HACC-R4、ADR-0031 第 1 點）。
-- 每個帳號每個供應商最多一個身分（HACC-R6）。attrs_json：trust_level、orgs。
CREATE TABLE identities (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id),
    provider       TEXT NOT NULL CHECK (provider IN ('google', 'github', 'linuxdo')),
    subject        TEXT NOT NULL,
    login          TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL DEFAULT '',
    email_verified INTEGER NOT NULL DEFAULT 0,
    attrs_json     TEXT NOT NULL DEFAULT '{}',
    linked_at      INTEGER NOT NULL,
    last_login_at  INTEGER NOT NULL,
    UNIQUE (provider, subject),
    UNIQUE (user_id, provider)
) STRICT;

-- +goose Down
DROP TABLE identities;
DROP TABLE users;
