-- +goose Up
-- M5-S2：群組成員與 Hub token（HACC-R9～R12）。時間欄位是 Unix 毫秒，取自注入的 clock。

-- group_members 是群組的成員資格（members 不存：每個帳號都在 members）。
-- source：manual（管理員指派）、rule（自動規則，rule_provider 是給出它的供應商，以該供應商登入時重算）、
-- config（設定檔的 admins，啟動與登入時同步）。同一個群組可以同時有多種來源；有效權限取聯集。
-- 設定檔已經沒有的群組列保留但不計入權限。
CREATE TABLE group_members (
    user_id       TEXT NOT NULL REFERENCES users (id),
    group_name    TEXT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('manual', 'rule', 'config')),
    rule_provider TEXT NOT NULL DEFAULT '' CHECK ((source = 'rule') = (rule_provider <> '')),
    added_at      INTEGER NOT NULL,
    PRIMARY KEY (user_id, group_name, source, rule_provider)
) STRICT;

CREATE INDEX group_members_group ON group_members (group_name, source);

-- pats 是 Hub token（HACC-R9）：只存 token 的 SHA-256；permissions 是 JSON 陣列。
-- 有效權限是 permissions 與帳號當下權限的交集（每個請求重算）。撤銷時 revoked_at 有值。
CREATE TABLE pats (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users (id),
    name        TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    permissions TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL,
    revoked_at  INTEGER
) STRICT;

CREATE INDEX pats_user ON pats (user_id);

-- +goose Down
DROP TABLE pats;
DROP TABLE group_members;
