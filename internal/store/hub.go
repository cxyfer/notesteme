package store

import (
	"context"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// HubShare 是一次成功的分享（HSHR-R2）。VersionsJSON 是 Hub 回傳的版本清單（JSON 陣列）。
type HubShare struct {
	ID           string
	ItemID       string
	HubURL       string
	ShareID      string
	VersionsJSON string
	CreatedAt    time.Time
}

// InsertHubShare 記錄一次分享；ID 空白時由 store 產生，created_at 是 store 的現在時間。
func (r *Repo) InsertHubShare(ctx context.Context, s HubShare) error {
	if s.ID == "" {
		s.ID = NewID()
	}
	if s.VersionsJSON == "" {
		s.VersionsJSON = "[]"
	}
	return r.q.InsertHubShare(ctx, sqlcdb.InsertHubShareParams{
		ID: s.ID, ItemID: s.ItemID, HubUrl: s.HubURL, ShareID: s.ShareID, VersionsJson: s.VersionsJSON, CreatedAt: r.nowMs(),
	})
}

// ItemHubShares 依時間列出影音的分享紀錄。
func (r *Repo) ItemHubShares(ctx context.Context, itemID string) ([]HubShare, error) {
	rows, err := r.q.ItemHubShares(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]HubShare, 0, len(rows))
	for _, row := range rows {
		out = append(out, HubShare{ID: row.ID, ItemID: row.ItemID, HubURL: row.HubUrl, ShareID: row.ShareID,
			VersionsJSON: row.VersionsJson, CreatedAt: fromMs(row.CreatedAt)})
	}
	return out, nil
}

// HubImport 是等待 transcript 步驟寫入的 Hub 字幕（HSHR-R9、R11）。
type HubImport struct {
	ItemID     string
	VersionID  string
	SHA256     string
	OriginJSON string
	Content    []byte
	CreatedAt  time.Time
}

// UpsertHubImport 記下（或取代）影音等待中的 Hub 字幕。
func (r *Repo) UpsertHubImport(ctx context.Context, h HubImport) error {
	return r.q.UpsertHubImport(ctx, sqlcdb.UpsertHubImportParams{
		ItemID: h.ItemID, VersionID: h.VersionID, Sha256: h.SHA256, OriginJson: h.OriginJSON, Content: h.Content, CreatedAt: r.nowMs(),
	})
}

// HubImport 取出影音等待中的 Hub 字幕；沒有時回傳 ErrNotFound。
func (r *Repo) HubImport(ctx context.Context, itemID string) (HubImport, error) {
	row, err := r.q.GetHubImport(ctx, itemID)
	if err != nil {
		return HubImport{}, notFound(err)
	}
	return HubImport{ItemID: row.ItemID, VersionID: row.VersionID, SHA256: row.Sha256, OriginJSON: row.OriginJson,
		Content: row.Content, CreatedAt: fromMs(row.CreatedAt)}, nil
}

// DeleteHubImport 刪除已經寫入的 Hub 字幕；之後又記下的另一份（sha256 不同）保留。
func (r *Repo) DeleteHubImport(ctx context.Context, itemID, sha256 string) error {
	return r.q.DeleteHubImport(ctx, sqlcdb.DeleteHubImportParams{ItemID: itemID, Sha256: sha256})
}
