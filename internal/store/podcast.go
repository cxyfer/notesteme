package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// maxFeedKeySuffix 是 feedKey 撞名時最多嘗試到的編號（slug-2 … slug-100）。
const maxFeedKeySuffix = 100

func channelFromRow(row sqlcdb.Channel) Channel {
	return Channel{ID: row.ID, Platform: row.Platform, ExternalID: row.ExternalID, Title: row.Title, URL: row.Url}
}

// ChannelByKey 以 (platform, external_id) 查詢頻道；不存在時回傳 ErrNotFound。
func (r *Repo) ChannelByKey(ctx context.Context, platform, externalID string) (Channel, error) {
	row, err := r.q.ChannelByKey(ctx, sqlcdb.ChannelByKeyParams{Platform: platform, ExternalID: externalID})
	if err != nil {
		return Channel{}, notFound(err)
	}
	return channelFromRow(row), nil
}

// PodcastFeedKey 回傳 feed 的 feedKey（podcast 頻道的 external_id），沒有的話建立頻道。
// feedURL 是正規化的 feed 網址（它是 feed 的身分）：已有頻道用同一個網址就沿用它的 key；
// 否則依序試 slug、slug-2 … slug-100，第一個還沒被別的 feed 佔用的建立成頻道（標題為 title）。
// 在 Store.Tx 的 callback 中用傳進來的 Repo 呼叫，和建立影音在同一個交易裡。
func (r *Repo) PodcastFeedKey(ctx context.Context, feedURL, slug, title string) (string, error) {
	switch row, err := r.q.PodcastChannelByURL(ctx, feedURL); {
	case err == nil:
		return row.ExternalID, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", err
	}
	if slug == "" {
		slug = "feed"
	}
	for n := 1; n <= maxFeedKeySuffix; n++ {
		key := slug
		if n > 1 {
			key = fmt.Sprintf("%s-%d", slug, n)
		}
		switch _, err := r.q.ChannelByKey(ctx, sqlcdb.ChannelByKeyParams{Platform: source.PlatformPodcast, ExternalID: key}); {
		case err == nil:
			continue // 被另一個 feed 佔用
		case !errors.Is(err, sql.ErrNoRows):
			return "", err
		}
		if _, err := r.UpsertChannel(ctx, Channel{Platform: source.PlatformPodcast, ExternalID: key, Title: title, URL: feedURL}); err != nil {
			return "", err
		}
		return key, nil
	}
	return "", fmt.Errorf("store: 找不到可用的 feed 代號（%s-2 … %s-%d 都已被使用）", slug, slug, maxFeedKeySuffix)
}

// SetItemChannel 讓影音指向頻道。
func (r *Repo) SetItemChannel(ctx context.Context, itemID, channelID string) error {
	n, err := r.q.SetItemChannel(ctx, sqlcdb.SetItemChannelParams{ChannelID: &channelID, ID: itemID})
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("更新影音 %s：%w", itemID, ErrNotFound)
	}
	return nil
}
