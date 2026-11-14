package harness

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// 事件持久化（OPS-R10）的驗收輔助：直接以另一條連線讀寫場景資料庫的 events 表。
// production 的 Server 不對外提供 store，harness 也不該為了測試開後門；
// SQLite 的 WAL 模式允許 server 執行中同時有其他連線讀寫（寫入等待 busy_timeout）。

// InsertEventAt 在 events 表加一筆指定時間的事件，模擬「過去已經持久化」的事件。
// 資料庫必須已存在（server 已啟動過），不會自己建立。
func (w *World) InsertEventAt(at time.Time) error {
	u := url.URL{Scheme: "file", Path: filepath.Join(w.DataDir(), "notesteme.db"), RawQuery: url.Values{
		"mode":    {"rw"}, // rw 不會建立檔案：資料庫不存在時直接失敗
		"_pragma": {"busy_timeout(5000)"},
	}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return fmt.Errorf("開啟資料庫：%w", err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx,
		`INSERT INTO events (id, item_id, type, payload_json, at) VALUES (?, '', 'test.event', '{}', ?)`,
		store.NewID(), at.UnixMilli())
	if err != nil {
		return fmt.Errorf("寫入事件：%w", err)
	}
	return nil
}

// EventCount 以唯讀連線回報 events 表的筆數。
func (w *World) EventCount() (n int, err error) {
	err = w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&n)
	})
	return n, err
}

// EventsBefore 以唯讀連線回報 at 早於 cutoff 的事件筆數。
func (w *World) EventsBefore(cutoff time.Time) (n int, err error) {
	err = w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE at < ?`, cutoff.UnixMilli()).Scan(&n)
	})
	return n, err
}
