package nfrbench

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/sqlitedb"
	"github.com/cxyfer/notesteme/internal/store"
)

// NFR-CMP-5：每個 migration 在 lib-10k 規模的資料上測試。
//
// 從空的 DB 逐版往前套用（goose UpByOne，與 serve 相同的 foreign_keys(1) 連線設定），每套用一版之前，
// 把這時已經存在、但還是空的資料表用 lib-10k 的資料填滿（ATTACH 產生的 DB，複製兩邊都有的欄位；
// FTS 的虛擬表與 shadow table 不直接複製，由 search_cues 的 trigger 建立）。所以每個 migration 執行時，
// 它之前就存在的資料表都有 lib-10k 規模的資料。最後檢查：
//   - PRAGMA integrity_check 是 ok、cues_fts 的 integrity-check 通過；
//   - 每個複製過的資料表，列數與來源相同（migration 沒有弄丟或複製出多餘的列）。
//
// 每版的耗時與總耗時只報告（規格沒有時間門檻）；判定的是「問題數 = 0」。
// 資料被 migration 預先寫入（例如內建樣式）的表不覆蓋也不比對列數。
//
// search_groups、search_text（SRCH S9）不是由 migration 而是由 migration 之後第一次 store.Open 從 search_cues 建立，
// 所以不複製：全部 migration 之後，以「索引已經建好、groups 還沒建立」的狀態執行一次 store.Open，
// 報告回填的耗時，並把 store 的一致性檢查（SearchIndexCheck）與列數比對算進問題數。
func BenchmarkCmp5Migrations(b *testing.B) {
	dir, name := lib(b)
	src := filepath.Join(dir, store.DBFileName)
	c := newChecker(b)
	for b.Loop() {
		work := shortDir(b)
		dstPath := filepath.Join(work, "migrate.db")
		db, err := sqlitedb.Open(dstPath, false)
		if err != nil {
			b.Fatal(err)
		}
		db.SetMaxOpenConns(1) // ATTACH 與 PRAGMA 都是連線層級
		ctx := context.Background()
		provider, err := goose.NewProvider(goose.DialectSQLite3, db, store.MigrationsFS())
		if err != nil {
			b.Fatal(err)
		}
		filled := map[string]int64{}
		var total, fillTotal time.Duration
		var slowest string
		var slowestDur time.Duration
		for {
			fstart := time.Now()
			if err := fillEmptyTables(ctx, db, src, filled); err != nil {
				b.Fatal(err)
			}
			fillTotal += time.Since(fstart)
			start := time.Now()
			res, err := provider.UpByOne(ctx)
			if err != nil {
				if errors.Is(err, goose.ErrNoNextVersion) {
					break
				}
				b.Fatalf("migration 失敗：%v", err)
			}
			d := time.Since(start)
			total += d
			v := fmt.Sprintf("%05d", res.Source.Version)
			fmt.Fprintf(os.Stderr, "    migration %s：%s\n", filepath.Base(res.Source.Path), d.Round(time.Millisecond))
			if d > slowestDur {
				slowest, slowestDur = v, d
			}
		}
		if err := fillEmptyTables(ctx, db, src, filled); err != nil {
			b.Fatal(err)
		}
		db, backfill, problems := backfillOnOpen(ctx, b, db, src, work, dstPath)
		problems = append(problems, checkMigrated(ctx, b, db, src, filled)...)
		_ = db.Close()
		b.ReportMetric(total.Seconds(), "migrations_s")
		b.ReportMetric(fillTotal.Seconds(), "fill_s")
		writeReport(b, result{ID: "NFR-CMP-5", Case: "all_migrations", Metric: "duration", Value: total.Seconds(), Unit: "s", Data: name, Pass: true,
			Note: fmt.Sprintf("（最慢 %s：%s；複製資料 %s；%d 個表）", slowest, slowestDur.Round(time.Millisecond), fillTotal.Round(time.Second), len(filled))})
		b.ReportMetric(backfill.Seconds(), "search_groups_backfill_s")
		writeReport(b, result{ID: "NFR-CMP-5", Case: "search_groups_backfill", Metric: "duration", Value: backfill.Seconds(), Unit: "s", Data: name, Pass: true,
			Note: "（migration 00038 之後第一次開啟，從 search_cues 建立 search_groups 與 search_text）"})
		fmt.Fprintf(os.Stderr, "    全部 migration %s（最慢 %s：%s），複製資料 %s\n", total.Round(time.Millisecond), slowest, slowestDur.Round(time.Millisecond), fillTotal.Round(time.Second))
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "    問題："+p)
		}
		c.check(result{ID: "NFR-CMP-5", Case: "integrity", Metric: "problems", Value: float64(len(problems)), Threshold: 1, Unit: "", Data: name,
			Note: strings.Join(problems, "；")})
		_ = os.Remove(dstPath)
	}
}

// derivedAtOpen 是第一次 store.Open 時才建立內容的資料表（SRCH S9），fillEmptyTables 不複製。
var derivedAtOpen = []string{"search_groups", "search_text"}

// backfillOnOpen 模擬既有的 DB 升級到 migration 00038 之後第一次開啟：search_cues 已經是建好的索引（built = 1）、
// groups_built = 0。關閉 db、以 store.Open 開啟資料目錄（回填），檢查之後重新開啟 db 回傳。
func backfillOnOpen(ctx context.Context, b *testing.B, db *sql.DB, src, work, dstPath string) (*sql.DB, time.Duration, []string) {
	b.Helper()
	var problems []string
	if _, err := db.ExecContext(ctx, `UPDATE search_index_state SET built = 1, groups_built = 0`); err != nil {
		b.Fatal(err)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
	dataDir := filepath.Join(work, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		b.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, store.DBFileName)
	if err := os.Rename(dstPath, dbPath); err != nil {
		b.Fatal(err)
	}
	start := time.Now()
	st, err := store.Open(ctx, dataDir, clock.Real{})
	d := time.Since(start)
	if err != nil {
		b.Fatalf("migration 之後第一次開啟失敗：%v", err)
	}
	if err := st.CheckSearchGroups(ctx); err != nil {
		problems = append(problems, "search_groups："+err.Error())
	}
	if err := st.Close(); err != nil {
		b.Fatal(err)
	}
	if err := os.Rename(dbPath, dstPath); err != nil {
		b.Fatal(err)
	}
	if db, err = sqlitedb.Open(dstPath, false); err != nil {
		b.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	detach, err := attachSrc(ctx, db, src)
	if err != nil {
		b.Fatal(err)
	}
	defer detach()
	for _, t := range derivedAtOpen {
		var got, want int64
		if err := db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM main."`+t+`"), (SELECT COUNT(*) FROM src."`+t+`")`).Scan(&got, &want); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", t, err))
		} else if got != want {
			problems = append(problems, fmt.Sprintf("%s 的列數 %d，來源 %d", t, got, want))
		}
	}
	return db, d, problems
}

// fillEmptyTables 把 main 中還是空的、src 也有的一般資料表，以兩邊都有的欄位從 src 複製過來。
// src 只在複製期間 ATTACH：它也有 goose_db_version，ATTACH 著的時候 goose 會讀到它的版本。
func fillEmptyTables(ctx context.Context, db *sql.DB, src string, filled map[string]int64) error {
	detach, err := attachSrc(ctx, db, src)
	if err != nil {
		return err
	}
	defer detach()
	tables, err := userTables(ctx, db, "main")
	if err != nil {
		return err
	}
	srcTables, err := userTables(ctx, db, "src")
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer func() { _, _ = db.ExecContext(ctx, `PRAGMA foreign_keys = ON`) }()
	for _, t := range tables {
		if _, done := filled[t]; done || !slices.Contains(srcTables, t) || slices.Contains(derivedAtOpen, t) {
			continue
		}
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM main."`+t+`"`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue // migration 預先寫入的資料（不覆蓋、不比對）
		}
		cols, err := commonColumns(ctx, db, t)
		if err != nil {
			return err
		}
		if len(cols) == 0 {
			continue
		}
		list := `"` + strings.Join(cols, `","`) + `"`
		res, err := db.ExecContext(ctx, `INSERT INTO main."`+t+`" (`+list+`) SELECT `+list+` FROM src."`+t+`"`)
		if err != nil {
			return fmt.Errorf("複製 %s：%w", t, err)
		}
		filled[t], _ = res.RowsAffected()
	}
	return nil
}

// userTables 列出一般資料表：不含 sqlite_ 內部表、goose 的版本表、虛擬表與它們的 shadow table。
func userTables(ctx context.Context, db *sql.DB, schema string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, COALESCE(sql, '') FROM `+schema+`.sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type tbl struct{ name, sql string }
	var all []tbl
	for rows.Next() {
		var t tbl
		if err := rows.Scan(&t.name, &t.sql); err != nil {
			return nil, err
		}
		all = append(all, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var virtual []string
	for _, t := range all {
		if strings.HasPrefix(strings.ToUpper(t.sql), "CREATE VIRTUAL TABLE") {
			virtual = append(virtual, t.name)
		}
	}
	var out []string
	for _, t := range all {
		if strings.HasPrefix(t.name, "sqlite_") || t.name == "goose_db_version" || slices.Contains(virtual, t.name) {
			continue
		}
		shadow := false
		for _, v := range virtual {
			if strings.HasPrefix(t.name, v+"_") {
				shadow = true
			}
		}
		if !shadow {
			out = append(out, t.name)
		}
	}
	return out, nil
}

func tableColumns(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, ?)`, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

func commonColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	dst, err := tableColumns(ctx, db, "main", table)
	if err != nil {
		return nil, err
	}
	src, err := tableColumns(ctx, db, "src", table)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range dst {
		if slices.Contains(src, c) {
			out = append(out, c)
		}
	}
	return out, nil
}

// checkMigrated 回傳發現的問題（空的代表通過）。
func checkMigrated(ctx context.Context, b *testing.B, db *sql.DB, src string, filled map[string]int64) []string {
	b.Helper()
	detach, err := attachSrc(ctx, db, src)
	if err != nil {
		b.Fatal(err)
	}
	defer detach()
	var problems []string
	var ic string
	if err := db.QueryRowContext(ctx, `PRAGMA main.integrity_check`).Scan(&ic); err != nil || ic != "ok" {
		problems = append(problems, fmt.Sprintf("integrity_check = %q（%v）", ic, err))
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO main.cues_fts (cues_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
		problems = append(problems, "cues_fts integrity-check："+err.Error())
	}
	names := make([]string, 0, len(filled))
	for t := range filled {
		names = append(names, t)
	}
	slices.Sort(names)
	for _, t := range names {
		var got, want int64
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM main."`+t+`"`).Scan(&got); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", t, err))
			continue
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM src."`+t+`"`).Scan(&want); err != nil {
			problems = append(problems, fmt.Sprintf("%s：%v", t, err))
			continue
		}
		if got != want {
			problems = append(problems, fmt.Sprintf("%s 的列數 %d，來源 %d", t, got, want))
		}
	}
	return problems
}

func attachSrc(ctx context.Context, db *sql.DB, src string) (func(), error) {
	if _, err := db.ExecContext(ctx, `ATTACH DATABASE ? AS src`, "file:"+src+"?mode=ro"); err != nil {
		return nil, err
	}
	return func() { _, _ = db.ExecContext(ctx, `DETACH DATABASE src`) }, nil
}
