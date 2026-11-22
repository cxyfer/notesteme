-- +goose Up
-- Files written by output targets (OUT-R11). One row per (item, file path): sha256 is the content notesteme
-- last wrote there, so a later write overwrites the file only while its current hash still matches.
-- A purged item takes its records with it (DEL); the written files themselves stay (DEL-R9).
CREATE TABLE outputs (
    item_id    TEXT NOT NULL REFERENCES items (id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    target     TEXT NOT NULL,
    format     TEXT NOT NULL,
    lang       TEXT NOT NULL,
    sha256     TEXT NOT NULL,
    size       INTEGER NOT NULL,
    written_at INTEGER NOT NULL,
    PRIMARY KEY (item_id, path)
) STRICT;

-- +goose Down
DROP TABLE outputs;
