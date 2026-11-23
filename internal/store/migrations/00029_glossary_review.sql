-- +goose Up
-- Term candidate review (GLOS-R1, R5; ADR-0021 section 7). A decision records the term actually added to the
-- glossary (decided_term), where it went (decided_scope: channel or global) and when (decided_at, unix ms).
-- A rejection records only decided_at. term itself never changes, so a decided term is not proposed again (COR C4).
ALTER TABLE glossary_candidates ADD COLUMN decided_term TEXT;
ALTER TABLE glossary_candidates ADD COLUMN decided_scope TEXT;
ALTER TABLE glossary_candidates ADD COLUMN decided_at INTEGER;

-- The review list across channels (GLOS-R5): filtered by state, oldest first.
CREATE INDEX glossary_candidates_state ON glossary_candidates (state, created_at, id);

-- +goose Down
DROP INDEX glossary_candidates_state;
ALTER TABLE glossary_candidates DROP COLUMN decided_at;
ALTER TABLE glossary_candidates DROP COLUMN decided_scope;
ALTER TABLE glossary_candidates DROP COLUMN decided_term;
