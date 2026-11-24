-- +goose Up
-- SEC-R12: the status of a platform cookie on the settings page.
-- last_used_at: the last yt-dlp call that was handed this cookie, whatever its result; NULL until then.
-- expired_at: a call with this cookie was told to sign in (the cookie no longer logs in); cleared only when
-- the cookie is uploaded again (many calls succeed without signing in, so a later success proves nothing).
ALTER TABLE secrets ADD COLUMN last_used_at INTEGER;
ALTER TABLE secrets ADD COLUMN expired_at INTEGER;

-- +goose Down
ALTER TABLE secrets DROP COLUMN expired_at;
ALTER TABLE secrets DROP COLUMN last_used_at;
