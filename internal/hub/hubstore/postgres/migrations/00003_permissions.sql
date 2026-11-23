-- +goose Up
-- M5-S2：群組成員與 Hub token（HACC-R9～R12），同 sqlite/migrations/00003_permissions.sql。
-- CHECK 有名稱：00005 依名稱換掉 source 的允許值。

CREATE TABLE group_members (
    user_id       TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    group_name    TEXT COLLATE "C" NOT NULL,
    source        TEXT COLLATE "C" NOT NULL CONSTRAINT group_members_source CHECK (source IN ('manual', 'rule', 'config')),
    rule_provider TEXT COLLATE "C" NOT NULL DEFAULT '' CONSTRAINT group_members_rule_provider CHECK ((source = 'rule') = (rule_provider <> '')),
    added_at      TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, group_name, source, rule_provider)
);

CREATE INDEX group_members_group ON group_members (group_name, source);

-- permissions 是 JSON 陣列。
CREATE TABLE pats (
    id          TEXT COLLATE "C" PRIMARY KEY,
    seq         BIGINT GENERATED ALWAYS AS IDENTITY,
    user_id     TEXT COLLATE "C" NOT NULL REFERENCES users (id),
    name        TEXT NOT NULL,
    token_hash  TEXT COLLATE "C" NOT NULL UNIQUE,
    permissions JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);

CREATE INDEX pats_user ON pats (user_id);

-- +goose Down
DROP TABLE pats;
DROP TABLE group_members;
