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

-- Active token names must be unique so revoking by name is unambiguous,
-- while allowing a name to be reused after the earlier token is revoked.
CREATE UNIQUE INDEX api_tokens_active_name ON api_tokens (name) WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX api_tokens_active_name;
DROP TABLE api_tokens;
