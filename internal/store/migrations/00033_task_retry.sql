-- +goose Up
-- The queue page lists stage runs that can be retried by hand (POST /api/tasks/:id/retry): failed and blocked use
-- stage_runs_attention, cancelled ones (skipped with reason cancelled) use this partial index.
CREATE INDEX stage_runs_cancelled ON stage_runs (finished_at) WHERE status = 'skipped' AND block_reason = 'cancelled';

-- +goose Down
DROP INDEX stage_runs_cancelled;
