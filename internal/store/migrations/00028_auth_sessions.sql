-- +goose Up
-- Session list, proxy and OIDC sign-in (AUTH-R14, R17, R19, R20; ADR-0021 section 7). Times are unix milliseconds.
-- method: password, proxy or oidc. proxy_user binds a proxy session to the header value it was created for.
ALTER TABLE sessions ADD COLUMN method TEXT NOT NULL DEFAULT 'password';
ALTER TABLE sessions ADD COLUMN last_seen_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sessions ADD COLUMN proxy_user TEXT NOT NULL DEFAULT '';
UPDATE sessions SET last_seen_at = created_at;

-- One row per identity that signed in through OIDC (AUTH-R19).
CREATE TABLE oidc_identities (
    issuer        TEXT    NOT NULL,
    subject       TEXT    NOT NULL,
    last_login_at INTEGER NOT NULL,
    PRIMARY KEY (issuer, subject)
) STRICT;

-- Pending OIDC sign-ins (AUTH-R17). state and the notesteme_oidc cookie are stored only as SHA-256 hashes;
-- a row is deleted when its callback arrives (single use) or once it has expired.
CREATE TABLE oidc_logins (
    state_hash    TEXT    PRIMARY KEY,
    cookie_hash   TEXT    NOT NULL,
    nonce         TEXT    NOT NULL,
    code_verifier TEXT    NOT NULL,
    next          TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX oidc_logins_expires_at ON oidc_logins (expires_at);

-- +goose Down
DROP INDEX oidc_logins_expires_at;
DROP TABLE oidc_logins;
DROP TABLE oidc_identities;
ALTER TABLE sessions DROP COLUMN proxy_user;
ALTER TABLE sessions DROP COLUMN last_seen_at;
ALTER TABLE sessions DROP COLUMN method;
