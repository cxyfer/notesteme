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
	// Kind 是 subtitle、note 或 asset（ADR-0020 第 8 點）；空字串寫入時視為 subtitle。
	Kind string
	// Vault 是 vault id（target 為 vault 時；Path 相對於 vault 根目錄），其他目標為空字串。
	Vault string
	// GitCommit 是 git vault 這次寫入所在的 commit；其他情況為空字串。
	GitCommit string
	// State 是最近一次發布時這個檔案的結果（VAULT-R5）：written、unchanged、saved_as_new、skipped_user_changed；
	// 空字串寫入時視為 written。
	State string
}

// 輸出檔在最近一次發布的結果（outputs.state，VAULT-R5）。
const (
	OutputStateWritten     = "written"
	OutputStateUnchanged   = "unchanged"
	OutputStateSavedAsNew  = "saved_as_new"
	OutputStateUserChanged = "skipped_user_changed"
)

// 輸出檔案的種類（outputs.kind，ADR-0020 第 8 點）。
const (
	OutputKindSubtitle = "subtitle"
	OutputKindNote     = "note"
	OutputKindAsset    = "asset"
)

// nullable 把空字串轉成 NULL。
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func outputFromRow(row sqlcdb.Output) Output {
	return Output{
		ItemID: row.ItemID, Path: row.Path, Target: row.Target, Format: row.Format, Lang: row.Lang,
		SHA256: row.Sha256, Size: row.Size, WrittenAt: fromMs(row.WrittenAt),
		Kind: row.Kind, Vault: deref(row.Vault), GitCommit: deref(row.GitCommit), State: row.State,
	}
}

// RecordOutput 記錄（或更新）一個寫出的檔案；寫入時間取 store 的時鐘。影音不存在時回傳 ErrNotFound。
func (r *Repo) RecordOutput(ctx context.Context, o Output) error {
	return r.inTx(ctx, func(tx *Repo) error {
		// 影音已被永久刪除時，外鍵會讓寫入失敗；先確認，才能回傳 ErrNotFound。
		if _, err := tx.q.ItemByID(ctx, o.ItemID); err != nil {
			return notFound(err)
		}
		kind := o.Kind
		if kind == "" {
			kind = OutputKindSubtitle
		}
		state := o.State
		if state == "" {
			state = OutputStateWritten
		}
		return tx.q.UpsertOutput(ctx, sqlcdb.UpsertOutputParams{
			ItemID: o.ItemID, Path: o.Path, Target: o.Target, Format: o.Format, Lang: o.Lang,
			Sha256: o.SHA256, Size: o.Size, WrittenAt: tx.nowMs(),
			Kind: kind, Vault: nullable(o.Vault), GitCommit: nullable(o.GitCommit), State: state,
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

// SetOutputState 只更新輸出紀錄的發布結果（VAULT-R5），不動 sha256；沒有紀錄時不做任何事。
func (r *Repo) SetOutputState(ctx context.Context, itemID, path, state string) error {
	return r.q.SetOutputState(ctx, sqlcdb.SetOutputStateParams{ItemID: itemID, Path: path, State: state})
}

// SetOutputCommit 記錄 vault 檔案所在的 git commit（VAULT-R7）；沒有紀錄時不做任何事。
func (r *Repo) SetOutputCommit(ctx context.Context, itemID, path, commit string) error {
	return r.q.SetOutputCommit(ctx, sqlcdb.SetOutputCommitParams{ItemID: itemID, Path: path, GitCommit: nullable(commit)})
}
