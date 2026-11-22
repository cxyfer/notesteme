-- +goose Up
-- Custom ASS styles (OUT-R6, OUT-R8). Built-in styles ship in the binary and are never stored
-- here; a custom style name must not equal a built-in name. style_json holds the primary line,
-- the translation line and the layout.
CREATE TABLE ass_styles (
    name       TEXT PRIMARY KEY,
    style_json TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE ass_styles;
