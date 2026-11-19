-- +goose Up
-- Resume condition of a blocked stage and encrypted secrets storage. Times are unix milliseconds from the injected clock.

-- resume_on is a machine-readable condition for leaving blocked, e.g. cookie:bilibili or cookie:youtube.
-- It is only non-empty while the stage is blocked.
ALTER TABLE stage_runs ADD COLUMN resume_on TEXT NOT NULL DEFAULT '';

-- secrets keeps ciphertext only (XChaCha20-Poly1305 with the data key); (kind, name) is e.g. (cookie, bilibili).
CREATE TABLE secrets (
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    nonce      BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (kind, name)
) STRICT;

-- +goose Down
DROP TABLE secrets;
ALTER TABLE stage_runs DROP COLUMN resume_on;
