package store

import (
	"context"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// 這些方法都掛在 Repo 上，所以在 Store.Tx 的 callback 中用傳進來的 Repo 呼叫，就和
// ItemByKey、InsertItemIfAbsent、DeleteTombstones 同在一個交易裡（IngestLocal 的用法見 IngestLocal 的說明）。

// ItemByLocalSHA 以內容 sha256 查詢本機影音（含垃圾桶中的）：只找 platform 為 local 且 content_sha256 非空的影音。
// 有多筆時回傳最早新增的；找不到（或 sha 為空）回傳 ErrNotFound。
func (r *Repo) ItemByLocalSHA(ctx context.Context, sha string) (Item, error) {
	if sha == "" {
		return Item{}, ErrNotFound
	}
	row, err := r.q.ItemByLocalSHA(ctx, sha)
	if err != nil {
		return Item{}, notFound(err)
	}
	return itemFromRow(row), nil
}

// LocalTombstoneBlocks 回報永久刪除的紀錄是否擋住這個本機檔案重新加入：
// tombstone 的 sha256 與 sha 相同，或同一個 external_id（part 0）的 tombstone sha256 為空
// （當時還沒算出雜湊就被刪除）。sha 為空時只比對後者。
func (r *Repo) LocalTombstoneBlocks(ctx context.Context, externalID, sha string) (bool, error) {
	return r.q.LocalTombstoneBlocks(ctx, sqlcdb.LocalTombstoneBlocksParams{Sha: sha, ExternalID: externalID})
}

// SetItemContentSHA 寫入影音的內容雜湊；已經有雜湊時不覆蓋（只在 content_sha256 為空時寫入）。
func (r *Repo) SetItemContentSHA(ctx context.Context, id, sha string) error {
	return r.q.SetItemContentSHA(ctx, sqlcdb.SetItemContentSHAParams{Sha: sha, ID: id})
}
