-- +goose Up

-- encryption_keys 存由 master key 加密的資料金鑰（信封加密，ADR-0013）。
-- 同一時間只有一把現行的金鑰（retired_at 為 NULL）。
CREATE TABLE encryption_keys (
    id          TEXT PRIMARY KEY,
    wrapped_key TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    retired_at  INTEGER
) STRICT;

CREATE UNIQUE INDEX encryption_keys_active ON encryption_keys ((1)) WHERE retired_at IS NULL;

-- +goose Down
DROP INDEX encryption_keys_active;
DROP TABLE encryption_keys;
