-- +goose Up
-- The state of each output file as of the last publish (VAULT-R5): written, unchanged, saved_as_new or
-- skipped_user_changed. Rows written before this migration were written by notesteme.
ALTER TABLE outputs ADD COLUMN state TEXT NOT NULL DEFAULT 'written';

-- +goose Down
ALTER TABLE outputs DROP COLUMN state;
