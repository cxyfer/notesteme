package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store/sqlcdb"
)

// planRecorder 是 sqlcdb.DBTX：不執行語句，改為對它跑 EXPLAIN QUERY PLAN 並記下每一列的 detail。
// 這樣檢查的是 sqlc 實際產生的語句，不是測試裡另外抄一份的文字。
type planRecorder struct {
	t       *testing.T
	db      *sql.DB
	details []string
}

type noResult struct{}

func (noResult) LastInsertId() (int64, error) { return 0, nil }
func (noResult) RowsAffected() (int64, error) { return 0, nil }

func (p *planRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	rows, err := p.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			return nil, err
		}
		p.details = append(p.details, detail)
	}
	return noResult{}, rows.Err()
}

func (p *planRecorder) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	p.t.Fatal("不預期呼叫 PrepareContext")
	return nil, nil
}

func (p *planRecorder) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	p.t.Fatal("不預期呼叫 QueryContext")
	return nil, nil
}

func (p *planRecorder) QueryRowContext(context.Context, string, ...any) *sql.Row {
	p.t.Fatal("不預期呼叫 QueryRowContext")
	return nil
}

// 永久刪除影音時依 item_id 刪除事件：events_item 是部分索引（item_id <> ''），
// 查詢要帶得出同樣的條件，SQLite 才會使用它，不會掃整張 events。
func TestPurgeDeleteEventsUsesItemIndex(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rec := &planRecorder{t: t, db: st.w}
	if _, err := sqlcdb.New(rec).PurgeDeleteEvents(context.Background(), "0192f3a0-0000-7000-8000-0000000000aa"); err != nil {
		t.Fatal(err)
	}
	if len(rec.details) == 0 {
		t.Fatal("沒有取得執行計畫")
	}
	used := false
	for _, d := range rec.details {
		t.Logf("PurgeDeleteEvents：%s", d)
		if strings.Contains(d, "events_item") {
			used = true
		}
		if strings.HasPrefix(d, "SCAN") && strings.Contains(d, "events") {
			t.Errorf("掃了整張 events：%s", d)
		}
	}
	if !used {
		t.Errorf("沒有使用 events_item：%v", rec.details)
	}
}
