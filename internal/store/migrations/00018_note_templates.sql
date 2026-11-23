-- +goose Up
-- Note templates (ADR-0020 section 5, NOTE-R11): every PATCH adds a version, like prompts. The built-in
-- template video-notes lives in the code and is never stored here, so builtin is always 0 for these rows.
-- rules_json holds the template rules (read_button, the checks to run).
CREATE TABLE note_templates (
    id         TEXT NOT NULL,
    version    INTEGER NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    builtin    INTEGER NOT NULL DEFAULT 0,
    body       TEXT NOT NULL,
    rules_json TEXT NOT NULL DEFAULT '{}',
    sha256     TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (id, version)
);

-- +goose Down
DROP TABLE note_templates;
