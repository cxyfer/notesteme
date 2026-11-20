-- +goose Up
-- API bearer tokens (AUTH-R7 to R10). Times are unix milliseconds from the injected clock.

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL DEFAULT '',
    scopes       TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    expires_at   INTEGER,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
) STRICT;

-- Valid (not revoked, not expired) token names must be unique so revoking by name is unambiguous,
-- while a name can be reused once the earlier token is revoked or has expired. Expiry depends on
-- the current time, which a partial index cannot express, so CreateAPIToken checks it in the same
-- write transaction (the store has a single write connection); this index only serves the lookup.
CREATE INDEX api_tokens_name ON api_tokens (name);

-- +goose Down
DROP INDEX api_tokens_name;
DROP TABLE api_tokens;
