-- +goose Up
-- Client side of the Hub (HSHR, ADR-0032 section 4). Times are unix milliseconds.

-- One row per successful share (HSHR-R2). versions_json is the Hub's answer:
-- [{kind, variant, lang, version_id, deduplicated}].
CREATE TABLE hub_shares (
    id            TEXT    NOT NULL PRIMARY KEY,
    item_id       TEXT    NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    hub_url       TEXT    NOT NULL,
    share_id      TEXT    NOT NULL,
    versions_json TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX hub_shares_item ON hub_shares (item_id, created_at);

-- A validated Hub transcript waiting for the item's next transcript run (HSHR-R9, R11).
-- The transcript stage writes it as the source variant and deletes the row. content is the
-- cues JSON exactly as served by the Hub (its sha256 matched the version); origin_json is
-- the provenance to record: {url, version_id, sha256, uploader, transcribe_model, ...}.
CREATE TABLE hub_imports (
    item_id     TEXT    NOT NULL PRIMARY KEY REFERENCES items (id) ON DELETE CASCADE,
    version_id  TEXT    NOT NULL,
    sha256      TEXT    NOT NULL,
    origin_json TEXT    NOT NULL,
    content     BLOB    NOT NULL,
    created_at  INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE hub_imports;
DROP TABLE hub_shares;
