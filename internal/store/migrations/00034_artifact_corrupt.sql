-- +goose Up
-- NFR-REL-2: an artifact whose file no longer matches its sha256 (or is missing) is marked corrupt when it is read or
-- reused. A corrupt artifact is never a cache hit again; the step that produced it is rerun.
ALTER TABLE artifacts ADD COLUMN corrupt_at INTEGER;

-- +goose Down
ALTER TABLE artifacts DROP COLUMN corrupt_at;
