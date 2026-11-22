-- +goose Up
-- Glossaries (COR-R4): the global glossary (scope 'global', scope_id 'default') and one per channel
-- (scope 'channel', scope_id is the channel id). terms_json is a JSON array of strings.
CREATE TABLE glossaries (
    scope      TEXT NOT NULL,
    scope_id   TEXT NOT NULL,
    terms_json TEXT NOT NULL DEFAULT '[]',
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (scope, scope_id)
) STRICT;

-- Term candidates (COR-R6): terms the model noticed while correcting a channel's videos.
-- state: pending | accepted | rejected. A term is stored once per channel whatever its state,
-- so an accepted or rejected term is never proposed again.
CREATE TABLE glossary_candidates (
    id         TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels (id) ON DELETE CASCADE,
    item_id    TEXT REFERENCES items (id) ON DELETE SET NULL,
    term       TEXT NOT NULL,
    state      TEXT NOT NULL DEFAULT 'pending',
    created_at INTEGER NOT NULL,
    UNIQUE (channel_id, term)
) STRICT;

CREATE INDEX glossary_candidates_channel ON glossary_candidates (channel_id, created_at);

-- Per-chunk detail of a fallback (for COR-R3: the similarity that made the chunk fall back).
ALTER TABLE llm_chunks ADD COLUMN detail_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE llm_chunks DROP COLUMN detail_json;
DROP TABLE glossary_candidates;
DROP TABLE glossaries;
