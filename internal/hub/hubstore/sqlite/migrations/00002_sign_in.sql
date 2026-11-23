-- +goose Up
-- M5-S1：登入（HACC-R2～R8）。時間欄位是 Unix 毫秒，取自注入的 clock。

-- oauth_states 是等待中的 OAuth 登入或連結（HACC-R2、R3）：只存 state 的 hash；
-- verifier 以 session 金鑰加密（verifier_nonce + verifier_sealed），nonce 是 Google 的 OIDC nonce（其他供應商為空字串）。
-- intent 為 link 時 user_id 是發起連結的帳號（HACC-R6）。used_at 有值代表已經用過（每個 state 只能使用一次）。
CREATE TABLE oauth_states (
    state_hash      TEXT PRIMARY KEY,
    provider        TEXT NOT NULL CHECK (provider IN ('google', 'github', 'linuxdo')),
    intent          TEXT NOT NULL CHECK (intent IN ('login', 'link')),
    user_id         TEXT REFERENCES users (id),
    verifier_nonce  BLOB NOT NULL,
    verifier_sealed BLOB NOT NULL,
    nonce           TEXT NOT NULL DEFAULT '',
    created_at      INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    used_at         INTEGER
) STRICT;

CREATE INDEX oauth_states_expires ON oauth_states (expires_at);

-- sessions 是瀏覽器的 session（HACC-R8）：只存 token 的 hash；閒置到 expires_at 失效，
-- authenticated_at 是這個 session 登入的時間（連結登入方式要在 10 分鐘內，HACC-R6）。
CREATE TABLE sessions (
    id               TEXT PRIMARY KEY,
    user_id          TEXT NOT NULL REFERENCES users (id),
    token_hash       TEXT NOT NULL UNIQUE,
    created_at       INTEGER NOT NULL,
    authenticated_at INTEGER NOT NULL,
    expires_at       INTEGER NOT NULL
) STRICT;

CREATE INDEX sessions_user ON sessions (user_id);

-- audit_log 是稽核紀錄（HACC-R17）；M5-S1 寫入 identity.link、identity.unlink，其他動作由後續切片加入。
-- actor_user_id 是執行者；顯示時取它當下的 display_name。
CREATE TABLE audit_log (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT NOT NULL REFERENCES users (id),
    action        TEXT NOT NULL,
    target_kind   TEXT NOT NULL,
    target_id     TEXT NOT NULL,
    detail_json   TEXT NOT NULL DEFAULT '{}',
    at            INTEGER NOT NULL
) STRICT;

CREATE INDEX audit_log_at ON audit_log (at);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE sessions;
DROP TABLE oauth_states;
