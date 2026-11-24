-- +goose Up
-- NFR-OBS-2 (PIPE-R16): the last log lines of the run that left a stage failed or blocked, secrets masked,
-- joined with newlines. NULL for every other state (and for runs from before this migration).
ALTER TABLE stage_runs ADD COLUMN log_excerpt TEXT;

-- +goose Down
ALTER TABLE stage_runs DROP COLUMN log_excerpt;
