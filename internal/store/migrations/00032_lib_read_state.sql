-- +goose Up
-- Read state of an item (LIB-R19): unread when created, changed only by mark_read / mark_unread.
ALTER TABLE items ADD COLUMN read_state TEXT NOT NULL DEFAULT 'unread' CHECK (read_state IN ('unread', 'read'));

-- The overview (LIB-R21) looks up running, blocked and failed stage runs; queries repeat the same IN term so the
-- partial index applies.
CREATE INDEX stage_runs_attention ON stage_runs (status) WHERE status IN ('running', 'blocked', 'failed');
-- Recently finished stage runs for the overview's recent_done (LIB-R21).
CREATE INDEX stage_runs_finished ON stage_runs (finished_at) WHERE finished_at IS NOT NULL;

-- +goose Down
DROP INDEX stage_runs_finished;
DROP INDEX stage_runs_attention;
ALTER TABLE items DROP COLUMN read_state;
