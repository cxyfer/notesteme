package store

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Output 是輸出目標寫出的一個檔案的紀錄（OUT-R11）：SHA256 是 notesteme 最後一次寫入時的內容雜湊。
type Output struct {
	ItemID    string
	Path      string
	Target    string
	Format    string
	Lang      string
	SHA256    string
	Size      int64
	WrittenAt time.Time
}

func outputFromRow(row sqlcdb.Output) Output {
	return Output{
		ItemID: row.ItemID, Path: row.Path, Target: row.Target, Format: row.Format, Lang: row.Lang,
		SHA256: row.Sha256, Size: row.Size, WrittenAt: fromMs(row.WrittenAt),
	}
}

// RecordOutput 記錄（或更新）一個寫出的檔案；寫入時間取 store 的時鐘。影音不存在時回傳 ErrNotFound。
func (r *Repo) RecordOutput(ctx context.Context, o Output) error {
	return r.inTx(ctx, func(tx *Repo) error {
		// 影音已被永久刪除時，外鍵會讓寫入失敗；先確認，才能回傳 ErrNotFound。
		if _, err := tx.q.ItemByID(ctx, o.ItemID); err != nil {
			return notFound(err)
		}
		return tx.q.UpsertOutput(ctx, sqlcdb.UpsertOutputParams{
			ItemID: o.ItemID, Path: o.Path, Target: o.Target, Format: o.Format, Lang: o.Lang,
			Sha256: o.SHA256, Size: o.Size, WrittenAt: tx.nowMs(),
		})
	})
}

// Output 以路徑查詢影音的輸出紀錄；沒有紀錄時回傳 ErrNotFound。
func (r *Repo) Output(ctx context.Context, itemID, path string) (Output, error) {
	row, err := r.q.GetOutput(ctx, sqlcdb.GetOutputParams{ItemID: itemID, Path: path})
	if err != nil {
		return Output{}, notFound(err)
	}
	return outputFromRow(row), nil
}

// Outputs 依寫入時間列出影音寫出的檔案。
func (r *Repo) Outputs(ctx context.Context, itemID string) ([]Output, error) {
	rows, err := r.q.ListOutputs(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]Output, 0, len(rows))
	for _, row := range rows {
		out = append(out, outputFromRow(row))
	}
	return out, nil
}
