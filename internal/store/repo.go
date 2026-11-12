package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// Repo 是影音、產出、步驟與任務的資料存取。它綁定在一個連線來源上：
//   - Store.Read：唯讀連線池，只能查詢；
//   - Store.Write：唯一的寫入連線，每個陳述句各自提交；
//   - Store.Tx 傳入的 Repo：同一個交易。
//
// 在 Store.Tx 的 callback 裡只能使用傳進來的 Repo；那時呼叫 Write 會等待同一個寫入連線而卡死。
type Repo struct {
	q     *sqlcdb.Queries
	clock clock.Clock
}

// Read 回傳唯讀的 Repo。
func (s *Store) Read() *Repo { return &Repo{q: s.rq, clock: s.clock} }

// Write 回傳寫入用的 Repo（單一陳述句，自動提交）。
func (s *Store) Write() *Repo { return &Repo{q: s.wq, clock: s.clock} }

// Tx 在一個交易中執行 fn；fn 回傳錯誤、panic 或 ctx 取消時回復。
// panic 時也必須回復並釋放唯一的寫入連線，否則之後的寫入會永遠卡住；回復後重新 panic。
func (s *Store) Tx(ctx context.Context, fn func(*Repo) error) (err error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback() // 失敗與 panic 都走這裡；panic 會在 defer 結束後繼續往上拋
		}
	}()
	if err := fn(&Repo{q: s.wq.WithTx(tx), clock: s.clock}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// NewID 產生 UUIDv7。
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		// NewV7 只在系統亂數來源失敗時出錯；沒有亂數就無法安全地繼續。
		panic(fmt.Errorf("store: 產生 UUIDv7 失敗：%w", err))
	}
	return id.String()
}

func (r *Repo) nowMs() int64 { return r.clock.Now().UnixMilli() }

// msPtr 把可為空的時間轉成資料庫的值（Unix 毫秒）。
func msPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

// msToTime 是 msPtr 的反向。
func msToTime(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := fromMs(*ms)
	return &t
}

// notFound 把「沒有資料列」轉成 ErrNotFound，其他錯誤原樣回傳。
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// deref 取出可為空欄位的值；NULL 視為空字串。
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
