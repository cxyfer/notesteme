-- +goose Up
-- Full-text search groups (SRCH S9, NFR-PERF-9). A group is the indexed transcript of one (item, lang): its cues sit in
-- search_cues at the contiguous ids first_id .. first_id + cues - 1, cue n at first_id + n - 1 (the store assigns
-- those ids itself). Search counts hits per group instead of per cue: the FTS path maps each hit rowid to its group
-- through the INTEGER PRIMARY KEY, and the 1-2 character scan reads search_text (every cue's text_fold of the group,
-- joined by U+001F, U+001F inside a text_fold written as U+001E) instead of every search_cues row.
-- Both tables are derived data. Existing indexes are filled by Go on the first Open after this migration (groups_built
-- = 0), from search_cues, without reading the artifact files; a group whose ids are not contiguous is rebuilt from its
-- artifact instead.
CREATE TABLE search_groups (
    first_id    INTEGER PRIMARY KEY,
    cues        INTEGER NOT NULL,
    item_id     TEXT    NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    artifact_id TEXT    NOT NULL REFERENCES artifacts (id) ON DELETE CASCADE,
    lang        TEXT    NOT NULL
) STRICT;

CREATE INDEX search_groups_item_lang ON search_groups (item_id, lang);
CREATE INDEX search_groups_artifact ON search_groups (artifact_id);

CREATE TABLE search_text (
    first_id INTEGER PRIMARY KEY REFERENCES search_groups (first_id) ON DELETE CASCADE,
    fold     TEXT    NOT NULL
) STRICT;

ALTER TABLE search_index_state ADD COLUMN groups_built INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE search_index_state DROP COLUMN groups_built;
DROP TABLE search_text;
DROP TABLE search_groups;
