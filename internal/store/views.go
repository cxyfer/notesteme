package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// ErrViewExists 表示已經有同名（不分大小寫）的檢視（LIB-R27）。
var ErrViewExists = errors.New("store: 已經有同名的檢視")

// ErrViewLimit 表示檢視的數量已經到上限（LIB-R27）。
var ErrViewLimit = errors.New("store: 檢視的數量已經到上限")

// SavedView 是媒體庫的一個儲存檢視（LIB-R26）：名稱與媒體庫的網址參數（JSON 物件）。
type SavedView struct {
	ID        string
	Name      string
	Query     json.RawMessage
	CreatedAt time.Time
}

// SavedViews 依建立順序列出所有檢視。
func (r *Repo) SavedViews(ctx context.Context) ([]SavedView, error) {
	rows, err := r.q.ListSavedViews(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SavedView, 0, len(rows))
	for _, row := range rows {
		out = append(out, SavedView{ID: row.ID, Name: row.Name, Query: json.RawMessage(row.QueryJson), CreatedAt: fromMs(row.CreatedAt)})
	}
	return out, nil
}

// InsertSavedView 在同一個交易中檢查數量上限並新增檢視；ID 與建立時間由 store 決定。
// 名稱不分大小寫重複時回傳 ErrViewExists，已經有 limit 個檢視時回傳 ErrViewLimit。
func (s *Store) InsertSavedView(ctx context.Context, name string, query json.RawMessage, limit int) (SavedView, error) {
	v := SavedView{ID: NewID(), Name: name, Query: query}
	err := s.Tx(ctx, func(r *Repo) error {
		n, err := r.q.CountSavedViews(ctx)
		if err != nil {
			return err
		}
		if int(n) >= limit {
			return ErrViewLimit
		}
		now := r.nowMs()
		v.CreatedAt = fromMs(now)
		err = r.q.InsertSavedView(ctx, sqlcdb.InsertSavedViewParams{
			ID: v.ID, Name: name, NameFold: strings.ToLower(name), QueryJson: string(query), CreatedAt: now,
		})
		if isUniqueConstraint(err) {
			return ErrViewExists
		}
		return err
	})
	if err != nil {
		return SavedView{}, err
	}
	return v, nil
}

// DeleteSavedView 刪除檢視；不存在時回傳 ErrNotFound。
func (r *Repo) DeleteSavedView(ctx context.Context, id string) error {
	n, err := r.q.DeleteSavedView(ctx, id)
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}
