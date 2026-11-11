package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/cxyfer/notesteme/internal/clock"
)

// dbtx 是 *sql.DB 與 *sql.Tx 共有的查詢介面。
type dbtx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Repo 是影音、產出、步驟與任務的資料存取。它綁定在一個連線來源上：
//   - Store.Read：唯讀連線池，只能查詢；
//   - Store.Write：唯一的寫入連線，每個陳述句各自提交；
//   - Store.Tx 傳入的 Repo：同一個交易。
//
// 在 Store.Tx 的 callback 裡只能使用傳進來的 Repo；那時呼叫 Write 會等待同一個寫入連線而卡死。
type Repo struct {
	q     dbtx
	clock clock.Clock
}

// Read 回傳唯讀的 Repo。
func (s *Store) Read() *Repo { return &Repo{q: s.r, clock: s.clock} }

// Write 回傳寫入用的 Repo（單一陳述句，自動提交）。
func (s *Store) Write() *Repo { return &Repo{q: s.w, clock: s.clock} }

// Tx 在一個交易中執行 fn；fn 回傳錯誤或 ctx 取消時回復。
func (s *Store) Tx(ctx context.Context, fn func(*Repo) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Repo{q: tx, clock: s.clock}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
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

// msPtr 把可為空的時間轉成資料庫的值。
func msPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// scanOne 取出單列；沒有資料時回傳 ErrNotFound。
func scanOne(row *sql.Row, dest ...any) error {
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
