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

// queryPlanRecorder 同 planRecorder，但記錄 QueryContext 的執行計畫，回傳沒有列的結果（sqlc 的迴圈不會掃描）。
type queryPlanRecorder struct{ planRecorder }

func (p *queryPlanRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if _, err := p.ExecContext(ctx, query, args...); err != nil {
		return nil, err
	}
	return p.db.QueryContext(ctx, "SELECT 1 WHERE 0")
}

// 總覽的查詢（LIB-R21）要用索引，不能掃整張 stage_runs 或 events：媒體庫大的時候它們是最大的表。
func TestOverviewQueriesUseIndexes(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	tests := []struct {
		name  string
		run   func(q *sqlcdb.Queries) error
		index string
	}{
		{"OverviewAttentionRuns", func(q *sqlcdb.Queries) error {
			_, err := q.OverviewAttentionRuns(ctx, "|running|")
			return err
		}, "stage_runs_attention"},
		{"OverviewEvents", func(q *sqlcdb.Queries) error {
			_, err := q.OverviewEvents(ctx, sqlcdb.OverviewEventsParams{Since: 1, PageSize: 50})
			return err
		}, "events_at"},
		{"OverviewRecentDone", func(q *sqlcdb.Queries) error {
			_, err := q.OverviewRecentDone(ctx, sqlcdb.OverviewRecentDoneParams{ScanLimit: RecentDoneScan, PageSize: 5})
			return err
		}, "stage_runs_finished"},
	}
	for _, tt := range tests {
		rec := &queryPlanRecorder{planRecorder{t: t, db: st.w}}
		if err := tt.run(sqlcdb.New(rec)); err != nil {
			t.Fatalf("%s：%v", tt.name, err)
		}
		used := false
		for _, d := range rec.details {
			t.Logf("%s：%s", tt.name, d)
			if strings.Contains(d, tt.index) {
				used = true
			}
			if strings.HasPrefix(d, "SCAN") && (strings.Contains(d, "stage_runs") || strings.Contains(d, "events")) && !strings.Contains(d, "INDEX") {
				t.Errorf("%s 掃了整張表：%s", tt.name, d)
			}
		}
		if !used {
			t.Errorf("%s 沒有使用 %s：%v", tt.name, tt.index, rec.details)
		}
	}
}

// 已讀狀態：新增時 unread；垃圾桶中的影音不能改；篩選只回傳符合的。
func TestItemReadState(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	w := st.Write()
	a, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "aaaaaaaaaaa", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := w.InsertItemIfAbsent(ctx, NewItem{Platform: "youtube", ExternalID: "bbbbbbbbbbb", URL: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ReadState != ReadUnread {
		t.Fatalf("新增時 = %q", a.ReadState)
	}
	if ok, err := w.SetItemReadState(ctx, a.ID, ReadRead); !ok || err != nil {
		t.Fatalf("標為已讀 = %v %v", ok, err)
	}
	if _, err := w.SetItemReadState(ctx, a.ID, "bogus"); err == nil {
		t.Error("CHECK 要拒絕不正確的值")
	}
	if ok, _ := w.SetItemReadState(ctx, "nope", ReadRead); ok {
		t.Error("不存在的影音")
	}
	if _, _, err := w.TrashItem(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.SetItemReadState(ctx, b.ID, ReadRead); ok {
		t.Error("垃圾桶中的影音不能改")
	}
	for state, want := range map[string]int{ReadRead: 1, ReadUnread: 0, "": 1} {
		n, err := st.Read().CountItems(ctx, ItemFilter{ReadState: state})
		if err != nil || n != want {
			t.Errorf("read_state=%q：%d %v", state, n, err)
		}
		page, err := st.Read().ListItemsPage(ctx, ItemFilter{ReadState: state}, nil, 10)
		if err != nil || len(page) != want {
			t.Errorf("列表 read_state=%q：%d %v", state, len(page), err)
		}
	}
	parts, err := st.Read().ItemsByExternalID(ctx, "youtube", "aaaaaaaaaaa")
	if err != nil || len(parts) != 1 || parts[0].ID != a.ID || parts[0].ReadState != ReadRead {
		t.Errorf("ItemsByExternalID = %+v %v", parts, err)
	}
}
