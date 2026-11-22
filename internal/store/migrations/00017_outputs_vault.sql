-- +goose Up
-- Vault outputs (ADR-0020 section 8, VAULT-R5): kind tells a subtitle from a note or an asset (an image the
-- note embeds); vault is the vault id for target 'vault' and NULL otherwise, with path relative to the vault
-- root; git_commit is the commit that write landed in for a git vault. Existing rows are subtitles.
ALTER TABLE outputs ADD COLUMN kind TEXT NOT NULL DEFAULT 'subtitle';
ALTER TABLE outputs ADD COLUMN vault TEXT;
ALTER TABLE outputs ADD COLUMN git_commit TEXT;

-- +goose Down
ALTER TABLE outputs DROP COLUMN git_commit;
ALTER TABLE outputs DROP COLUMN vault;
ALTER TABLE outputs DROP COLUMN kind;
