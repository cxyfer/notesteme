-- +goose Up
-- Full-text search over transcripts (SRCH-R1, R2, R3, R8; ADR-0021 section 7). One row per cue of the single
-- indexed transcript per (item, lang). text_fold is text lowered by Go's strings.ToLower (SRCH S3), so both the
-- trigram index and the instr scan compare the same string. Rows go with their item or artifact (ON DELETE CASCADE),
-- and the triggers below keep the external-content FTS table in step, cascades included.
CREATE TABLE search_cues (
    id          INTEGER PRIMARY KEY,
    item_id     TEXT    NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    artifact_id TEXT    NOT NULL REFERENCES artifacts (id) ON DELETE CASCADE,
    lang        TEXT    NOT NULL,
    variant     TEXT    NOT NULL,
    cue         INTEGER NOT NULL,
    start_ms    INTEGER NOT NULL,
    end_ms      INTEGER NOT NULL,
    text        TEXT    NOT NULL,
    text_fold   TEXT    NOT NULL,
    UNIQUE (artifact_id, cue)
) STRICT;

CREATE INDEX search_cues_item_lang ON search_cues (item_id, lang);

CREATE VIRTUAL TABLE cues_fts USING fts5(
    text_fold, content = 'search_cues', content_rowid = 'id', tokenize = 'trigram case_sensitive 1'
);

-- +goose StatementBegin
CREATE TRIGGER search_cues_ai AFTER INSERT ON search_cues BEGIN
    INSERT INTO cues_fts (rowid, text_fold) VALUES (new.id, new.text_fold);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER search_cues_ad AFTER DELETE ON search_cues BEGIN
    INSERT INTO cues_fts (cues_fts, rowid, text_fold) VALUES ('delete', old.id, old.text_fold);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER search_cues_au AFTER UPDATE ON search_cues BEGIN
    INSERT INTO cues_fts (cues_fts, rowid, text_fold) VALUES ('delete', old.id, old.text_fold);
    INSERT INTO cues_fts (rowid, text_fold) VALUES (new.id, new.text_fold);
END;
-- +goose StatementEnd

-- Transcript contents live in artifact files, which SQL cannot read: the first Open after this migration builds the
-- index from the current transcripts in Go and sets built to 1 (SRCH S8).
CREATE TABLE search_index_state (
    id    INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
    built INTEGER NOT NULL
) STRICT;

INSERT INTO search_index_state (id, built) VALUES (1, 0);

-- +goose Down
DROP TABLE search_index_state;
DROP TRIGGER search_cues_au;
DROP TRIGGER search_cues_ad;
DROP TRIGGER search_cues_ai;
DROP TABLE cues_fts;
DROP TABLE search_cues;
