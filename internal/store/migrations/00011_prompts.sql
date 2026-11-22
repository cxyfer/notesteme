-- +goose Up
-- User prompts (PRM). Built-in prompts ship in the binary and are never stored here;
-- a user prompt id must not equal a built-in id.
-- default_version is what "<id>" and "<id>@default" resolve to; it only changes via
-- POST /api/prompts/:id/default (PRM-R5). latest_version is the highest saved version.
CREATE TABLE prompts (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,
    default_version INTEGER NOT NULL,
    latest_version  INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
) STRICT;

-- Immutable versions (PRM-R2). body is stored with CRLF normalised to LF; sha256 is over body.
-- variables_json lists the {{variables}} the body uses, in first-appearance order.
CREATE TABLE prompt_versions (
    prompt_id      TEXT NOT NULL REFERENCES prompts (id) ON DELETE CASCADE,
    version        INTEGER NOT NULL,
    body           TEXT NOT NULL,
    sha256         TEXT NOT NULL,
    variables_json TEXT NOT NULL DEFAULT '[]',
    created_at     INTEGER NOT NULL,
    PRIMARY KEY (prompt_id, version)
) STRICT;

-- +goose Down
DROP TABLE prompt_versions;
DROP TABLE prompts;
