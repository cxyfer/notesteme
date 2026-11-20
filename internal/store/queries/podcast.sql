-- name: PodcastChannelByURL :one
-- The podcast channel whose feed URL is exactly the given canonical URL (oldest first if there are several).
SELECT * FROM channels
WHERE platform = 'podcast' AND url = sqlc.arg(url)
ORDER BY rowid
LIMIT 1;

-- name: ChannelByKey :one
SELECT * FROM channels WHERE platform = sqlc.arg(platform) AND external_id = sqlc.arg(external_id);

-- name: SetItemChannel :execrows
-- Point an item at its channel; used right after the item is created.
UPDATE items SET channel_id = sqlc.arg(channel_id) WHERE id = sqlc.arg(id);
