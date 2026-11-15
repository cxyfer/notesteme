-- +goose Up
-- Library list (LIB-R1, R3, R4, R7).

-- title_fold is the title lowercased by Go (strings.ToLower) when it is written; the title search matches it with instr().
ALTER TABLE items ADD COLUMN title_fold TEXT NOT NULL DEFAULT '';
-- Best effort for rows written before this migration (ASCII only; later writes use the Go fold).
UPDATE items SET title_fold = lower(title) WHERE title <> '';

-- timings_json is the sorted, distinct list of timing origins found in the cues of a transcript artifact.
ALTER TABLE artifacts ADD COLUMN timings_json TEXT NOT NULL DEFAULT '[]';

-- Keyset pagination order of the list: created_at DESC, id DESC (an index is read backwards just as well).
CREATE INDEX items_created ON items (created_at, id);

-- The latest run of a stage (ORDER BY created_at DESC, rowid DESC LIMIT 1) is one backwards index step.
CREATE INDEX stage_runs_latest ON stage_runs (item_id, stage, created_at);
-- stage_runs_item (item_id, stage) is a prefix of stage_runs_latest and stage_runs_cache: every lookup it served
-- (by item_id, or by item_id and stage) is answered by those two, so it only slows down writes.
DROP INDEX stage_runs_item;

-- +goose Down
CREATE INDEX stage_runs_item ON stage_runs (item_id, stage);
DROP INDEX stage_runs_latest;
DROP INDEX items_created;
ALTER TABLE artifacts DROP COLUMN timings_json;
ALTER TABLE items DROP COLUMN title_fold;
