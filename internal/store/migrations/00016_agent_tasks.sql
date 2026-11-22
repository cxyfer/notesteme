-- +goose Up
-- Agent tasks (AGT): an agent claims LLM chunk tasks (executor 'agent') over MCP.
-- agent_failures counts consecutive validation failures under the current lease (AGT-R6); a new
-- claim resets it. release_reason is the reason the last holder gave when releasing (AGT-R8).
ALTER TABLE tasks ADD COLUMN agent_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN release_reason TEXT NOT NULL DEFAULT '';

-- Who produced a chunk's output (AGT-R10): executor is 'api' or 'agent'; for an agent, agent is the
-- token name and model_reported the model the agent said it used (may be empty, never verified).
ALTER TABLE llm_chunks ADD COLUMN executor TEXT NOT NULL DEFAULT 'api';
ALTER TABLE llm_chunks ADD COLUMN agent TEXT NOT NULL DEFAULT '';
ALTER TABLE llm_chunks ADD COLUMN model_reported TEXT NOT NULL DEFAULT '';

-- The sweeper looks up agent tasks by lease expiry and by claim deadline.
CREATE INDEX tasks_agent ON tasks (executor, state, lease_until, deadline_at);

-- +goose Down
DROP INDEX tasks_agent;
ALTER TABLE llm_chunks DROP COLUMN model_reported;
ALTER TABLE llm_chunks DROP COLUMN agent;
ALTER TABLE llm_chunks DROP COLUMN executor;
ALTER TABLE tasks DROP COLUMN release_reason;
ALTER TABLE tasks DROP COLUMN agent_failures;
