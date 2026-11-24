-- +goose Up
-- LIB-R26 to R28: saved views of the library. query_json keeps the library URL parameters (filters and
-- grouping, as the web UI writes them), validated by the API; name_fold is the name lowercased with Go's
-- strings.ToLower, so two names that differ only in case are the same view.
CREATE TABLE saved_views (
    id         TEXT PRIMARY KEY,
    name       TEXT    NOT NULL,
    name_fold  TEXT    NOT NULL UNIQUE,
    query_json TEXT    NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;

-- +goose Down
DROP TABLE saved_views;
