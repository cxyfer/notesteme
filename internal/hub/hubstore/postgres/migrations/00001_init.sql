-- +goose Up
-- Hub 的 Postgres schema（ADR-0030 第 2 點、ADR-0031 第 5 點），與 sqlite/migrations 一對一（版本號相同、語意相同）。
-- 時間是 timestamptz（寫入前截到毫秒，與 SQLite 的 Unix 毫秒一致）、布林是 boolean。
-- ID、雜湊與排序用的文字欄位一律 COLLATE "C"：比較與排序以位元組為準（同 SQLite 的 BINARY），不受資料庫 locale 影響。
-- seq 是插入順序（SQLite 的 rowid），同一毫秒內的排序依它。

CREATE TABLE users (
    id               TEXT COLLATE "C" PRIMARY KEY,
    display_name     TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL,
    suspended_at     TIMESTAMPTZ,
    suspended_reason TEXT NOT NULL DEFAULT ''
);

CREATE TABLE identities (
    id             TEXT COLLATE "C" PRIMARY KEY,
    seq            BIGINT GENERATED ALWAYS AS IDENTITY,
    user_id        TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    provider       TEXT COLLATE "C" NOT NULL CHECK (provider IN ('google', 'github', 'linuxdo')),
    subject        TEXT COLLATE "C" NOT NULL,
    login          TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL DEFAULT '',
    email_verified BOOLEAN NOT NULL DEFAULT FALSE,
    attrs_json     JSONB NOT NULL DEFAULT '{}',
    linked_at      TIMESTAMPTZ NOT NULL,
    last_login_at  TIMESTAMPTZ NOT NULL,
    CONSTRAINT identities_provider_subject UNIQUE (provider, subject),
    CONSTRAINT identities_user_provider UNIQUE (user_id, provider)
);

-- +goose Down
DROP TABLE identities;
DROP TABLE users;
