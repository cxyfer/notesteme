-- +goose Up
-- M5-S1：登入（HACC-R2～R8），同 sqlite/migrations/00002_sign_in.sql。

CREATE TABLE oauth_states (
    state_hash      TEXT COLLATE "C" PRIMARY KEY,
    provider        TEXT COLLATE "C" NOT NULL CHECK (provider IN ('google', 'github', 'linuxdo')),
    intent          TEXT NOT NULL CHECK (intent IN ('login', 'link')),
    user_id         TEXT COLLATE "C" REFERENCES users (id),
    verifier_nonce  BYTEA NOT NULL,
    verifier_sealed BYTEA NOT NULL,
    nonce           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ
);

CREATE INDEX oauth_states_expires ON oauth_states (expires_at);

CREATE TABLE sessions (
    id               TEXT COLLATE "C" PRIMARY KEY,
    user_id          TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    token_hash       TEXT COLLATE "C" NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL,
    authenticated_at TIMESTAMPTZ NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_user ON sessions (user_id);

-- actor_user_id 在 00005 改成可以是 NULL（命令列的動作）。
CREATE TABLE audit_log (
    id            TEXT COLLATE "C" PRIMARY KEY,
    seq           BIGINT GENERATED ALWAYS AS IDENTITY,
    actor_user_id TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    action        TEXT NOT NULL,
    target_kind   TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    detail_json   JSONB NOT NULL DEFAULT '{}',
    at            TIMESTAMPTZ NOT NULL
);

CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE oauth_states;
