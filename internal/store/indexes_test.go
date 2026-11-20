package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// indexColumns 回傳資料表每個索引的欄位（依順序）。
func indexColumns(t *testing.T, db *sql.DB, table string) map[string][]string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM pragma_index_list(?)", table)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, n := range names {
		crow, err := db.Query("SELECT name FROM pragma_index_info(?) ORDER BY seqno", n)
		if err != nil {
			t.Fatal(err)
		}
		for crow.Next() {
			var c string
			if err := crow.Scan(&c); err != nil {
				t.Fatal(err)
			}
			out[n] = append(out[n], c)
		}
		if err := crow.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// stage_runs 不留是其他索引前綴的索引：多餘的索引只會拖慢每次寫入。
func TestStageRunsHasNoRedundantIndex(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	idx := indexColumns(t, st.w, "stage_runs")
	for a, ca := range idx {
		if strings.HasPrefix(a, "sqlite_autoindex") {
			continue
		}
		for b, cb := range idx {
			if a != b && len(ca) < len(cb) && slices.Equal(ca, cb[:len(ca)]) {
				t.Errorf("索引 %s %v 是 %s %v 的前綴，多餘", a, ca, b, cb)
			}
		}
	}
}

// 依 stage_runs 查詢的執行計畫：每一個都要走索引，不能掃整張表。
func TestStageRunsQueriesUseIndexes(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir(), clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	queries := map[string]string{
		"StageRunsByItem":     "SELECT * FROM stage_runs WHERE item_id = ? ORDER BY created_at, rowid",
		"DoneStageByCacheKey": "SELECT * FROM stage_runs WHERE item_id = ? AND stage = ? AND cache_key = ? AND status = 'done' ORDER BY created_at DESC, rowid DESC LIMIT 1",
		"ExecutedRunsByItem":  "SELECT sr.id FROM stage_runs sr JOIN config_snapshots cs ON cs.id = sr.config_snapshot_id WHERE sr.item_id = ? AND sr.started_at IS NOT NULL ORDER BY sr.started_at DESC, sr.rowid DESC",
		"LatestRunStatus":     "SELECT r.status FROM stage_runs AS r WHERE r.item_id = ? AND r.stage = ? ORDER BY r.created_at DESC, r.rowid DESC LIMIT 1",
		"TasksOfItem":         "SELECT id FROM stage_runs WHERE item_id = ?",
	}
	for name, q := range queries {
		args := make([]any, strings.Count(q, "?"))
		for i := range args {
			args[i] = "x"
		}
		rows, err := st.w.Query("EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatalf("%s：%v", name, err)
		}
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s：%s", name, detail)
			if (strings.Contains(detail, "stage_runs") || strings.Contains(detail, " sr")) && strings.HasPrefix(detail, "SCAN") {
				t.Errorf("%s 掃了整張表：%s", name, detail)
			}
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
