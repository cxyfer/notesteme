-- +goose Up
-- Cost ledger, price overrides and the budget (COST, ADR-0021 sections 4 and 7). Times are unix milliseconds.
--
-- cost_ledger already exists (00007). This adds stage: the stage run a row points to is deleted with its item
-- (DEL-R7), but grouping by stage and the CSV export still need it. Rows written before 00023 have stage ''.
ALTER TABLE cost_ledger ADD COLUMN stage TEXT NOT NULL DEFAULT '';

-- Month and range queries filter on at; the budget check sums the reservations (COST-R6).
CREATE INDEX cost_ledger_at ON cost_ledger (at);
CREATE INDEX cost_ledger_reserved ON cost_ledger (state) WHERE state = 'reserved';
-- attempt is the number of rows of the task so far plus one (COST-R1).
CREATE INDEX cost_ledger_task ON cost_ledger (task_id) WHERE task_id IS NOT NULL;

-- User overrides of the built-in prices (COST-R2, R12). checked_at is unix milliseconds.
CREATE TABLE price_overrides (
    provider     TEXT    NOT NULL,
    model        TEXT    NOT NULL,
    unit_type    TEXT    NOT NULL,
    usd_per_unit REAL    NOT NULL,
    checked_at   INTEGER NOT NULL,
    PRIMARY KEY (provider, model, unit_type)
) STRICT;

-- +goose Down
DROP TABLE price_overrides;
DROP INDEX cost_ledger_task;
DROP INDEX cost_ledger_reserved;
DROP INDEX cost_ledger_at;
ALTER TABLE cost_ledger DROP COLUMN stage;
