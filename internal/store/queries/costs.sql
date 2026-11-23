-- Cost ledger, price overrides and budget sums (COST). Amounts are summed as integer micro-USD
-- (ROUND(usd * 1e6)) so that 0.10 + 0.20 is exactly 0.30 (COST rules, C10).

-- name: InsertCostRow :exec
INSERT INTO cost_ledger (id, item_id, item_title, stage_run_id, task_id, stage, provider, model, units, unit_type,
                         usd, state, attempt, outcome, unit_price_json, at)
VALUES (sqlc.arg(id), sqlc.narg(item_id), sqlc.arg(item_title), sqlc.narg(stage_run_id), sqlc.narg(task_id),
        sqlc.arg(stage), sqlc.arg(provider), sqlc.arg(model), sqlc.arg(units), sqlc.arg(unit_type), sqlc.arg(usd),
        sqlc.arg(state), sqlc.arg(attempt), sqlc.arg(outcome), sqlc.arg(unit_price_json), sqlc.arg(at));

-- name: CountTaskCostRows :one
SELECT COUNT(*) FROM cost_ledger WHERE task_id = sqlc.arg(task_id);

-- name: SettleCostRow :execrows
-- Only a reservation can be settled: a row already settled, or deleted by the restart cleanup, is left alone.
UPDATE cost_ledger
SET state = sqlc.arg(state), usd = sqlc.arg(usd), units = sqlc.arg(units), unit_type = sqlc.arg(unit_type),
    outcome = sqlc.arg(outcome), unit_price_json = sqlc.arg(unit_price_json)
WHERE id = sqlc.arg(id) AND state = 'reserved';

-- name: DeleteCostReservation :execrows
DELETE FROM cost_ledger WHERE id = sqlc.arg(id) AND state = 'reserved';

-- name: DeleteOrphanReservations :execrows
-- A reservation belongs to a task that is running right now, or to a request of a batch that has not ended
-- (BATCH-R10: also when the task left the batch, the result is still charged, BATCH-R9); any other one was left
-- by a crash (COST-R5, PIPE-R11).
DELETE FROM cost_ledger
WHERE state = 'reserved'
  AND (task_id IS NULL OR task_id NOT IN (SELECT t.id FROM tasks AS t WHERE t.state = 'leased'))
  AND (task_id IS NULL OR task_id NOT IN (SELECT t.id FROM tasks AS t JOIN batches AS b ON b.id = t.batch_id
                                          WHERE b.state IN ('submitted', 'cancelling')));

-- name: SumCostMicrosBetween :one
SELECT CAST(COALESCE(SUM(CASE WHEN state = 'actual' THEN CAST(ROUND(usd * 1000000) AS INTEGER) ELSE 0 END), 0) AS INTEGER) AS actual,
       CAST(COALESCE(SUM(CASE WHEN state = 'reserved' THEN CAST(ROUND(usd * 1000000) AS INTEGER) ELSE 0 END), 0) AS INTEGER) AS reserved
FROM cost_ledger
WHERE at >= sqlc.arg(from_ms) AND at < sqlc.arg(to_ms) AND state IN ('actual', 'reserved');

-- name: SumItemCostMicros :one
-- Every month and every rerun of the item counts against the per-item cap (COST-R6, C3).
SELECT CAST(COALESCE(SUM(CAST(ROUND(usd * 1000000) AS INTEGER)), 0) AS INTEGER) AS total
FROM cost_ledger
WHERE item_id = sqlc.arg(item_id) AND state IN ('actual', 'reserved');

-- name: ListItemCostRows :many
SELECT * FROM cost_ledger WHERE item_id = sqlc.arg(item_id) ORDER BY at, rowid;

-- name: ListCostRowsBetween :many
-- Newest first. The cursor is (at, id) of the last row of the previous page; cursor_at is a huge number for the
-- first page. The title is the current title while the item exists, otherwise the one kept at purge (DEL-R7).
SELECT c.id, c.item_id, c.stage, c.provider, c.model, c.units, c.unit_type, c.usd, c.state, c.attempt, c.outcome,
       c.unit_price_json, c.at,
       CAST(COALESCE(i.title, c.item_title) AS TEXT) AS title,
       CAST(COALESCE(i.platform, '') AS TEXT) AS platform,
       CAST(COALESCE(i.external_id, '') AS TEXT) AS external_id,
       CAST(COALESCE(i.part, 0) AS INTEGER) AS part,
       CAST(COALESCE(ch.title, '') AS TEXT) AS channel
FROM cost_ledger AS c
LEFT JOIN items AS i ON i.id = c.item_id
LEFT JOIN channels AS ch ON ch.id = i.channel_id
WHERE c.at >= sqlc.arg(from_ms) AND c.at < sqlc.arg(to_ms)
  AND (c.at < sqlc.arg(cursor_at) OR (c.at = sqlc.arg(cursor_at) AND c.id < sqlc.arg(cursor_id)))
ORDER BY c.at DESC, c.id DESC
LIMIT sqlc.arg(max);

-- name: CountCostRowsBetween :one
SELECT COUNT(*) FROM cost_ledger WHERE at >= sqlc.arg(from_ms) AND at < sqlc.arg(to_ms);

-- name: UpsertPriceOverride :exec
INSERT INTO price_overrides (provider, model, unit_type, usd_per_unit, checked_at)
VALUES (sqlc.arg(provider), sqlc.arg(model), sqlc.arg(unit_type), sqlc.arg(usd_per_unit), sqlc.arg(checked_at))
ON CONFLICT (provider, model, unit_type) DO UPDATE SET usd_per_unit = excluded.usd_per_unit, checked_at = excluded.checked_at;

-- name: DeletePriceOverride :execrows
DELETE FROM price_overrides WHERE provider = sqlc.arg(provider) AND model = sqlc.arg(model) AND unit_type = sqlc.arg(unit_type);

-- name: GetPriceOverride :one
SELECT * FROM price_overrides WHERE provider = sqlc.arg(provider) AND model = sqlc.arg(model) AND unit_type = sqlc.arg(unit_type);

-- name: ListPriceOverrides :many
SELECT * FROM price_overrides ORDER BY provider, model, unit_type;
