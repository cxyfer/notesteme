package harness

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
)

// 這個檔案放 schema 與 migration 的場景支援（OPS-R3、R4）：模擬「程式內建的最新版本是 N」、
// 「migration N 會失敗」、以指定版本建立資料庫，以及比對資料庫的 schema。語意見 docs/specs/steps.md 第 4.12 節。

var migrationName = regexp.MustCompile(`^(\d+)_.*\.sql$`)

// builtinMigrations 回傳內建 migration 的版本與檔名，依版本排序。
func builtinMigrations() (versions []int, names map[int]string, err error) {
	entries, err := fs.ReadDir(store.MigrationsFS(), ".")
	if err != nil {
		return nil, nil, err
	}
	names = map[int]string{}
	for _, e := range entries {
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, _ := strconv.Atoi(m[1])
		versions = append(versions, v)
		names[v] = e.Name()
	}
	slices.Sort(versions)
	return versions, names, nil
}

// syntheticMigration 是補在內建版本之後的合成 migration，讓版本可以到 N。
func syntheticMigration(v int) string {
	return fmt.Sprintf("-- +goose Up\nCREATE TABLE synthetic_v%d (id INTEGER);\n-- +goose Down\nDROP TABLE synthetic_v%d;\n", v, v)
}

// failingMigration 是會失敗的 migration：NO TRANSACTION，先建立 marker 表再失敗，
// 所以失敗時只有還原快照才能把資料庫恢復乾淨。
func failingMigration(v int) string {
	return fmt.Sprintf("-- +goose NO TRANSACTION\n-- +goose Up\nCREATE TABLE migration_%d_marker (id INTEGER);\nINSERT INTO migration_%d_missing_table VALUES (1);\n-- +goose Down\nDROP TABLE migration_%d_marker;\n", v, v, v)
}

// migrationsUpTo 回傳版本 1 到 upTo 的 migration：內建的在前，upTo 超過內建最新版本時補合成的。
// failAt 大於 0 時，該版本的 migration 換成會失敗的那一支。
func migrationsUpTo(upTo, failAt int) (fs.FS, error) {
	versions, names, err := builtinMigrations()
	if err != nil {
		return nil, err
	}
	out := fstest.MapFS{}
	for v := 1; v <= upTo; v++ {
		var data string
		name, builtin := names[v]
		if builtin {
			b, err := fs.ReadFile(store.MigrationsFS(), name)
			if err != nil {
				return nil, err
			}
			data = string(b)
		} else {
			if len(versions) > 0 && v < versions[len(versions)-1] {
				return nil, fmt.Errorf("內建的 migration 缺少版本 %d", v)
			}
			name, data = fmt.Sprintf("%05d_synthetic.sql", v), syntheticMigration(v)
		}
		if v == failAt {
			name, data = fmt.Sprintf("%05d_fails.sql", v), failingMigration(v)
		}
		out[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return out, nil
}

// latestBuiltin 是內建的最新 schema 版本。
func latestBuiltin() (int, error) {
	versions, _, err := builtinMigrations()
	if err != nil || len(versions) == 0 {
		return 0, errors.Join(err, errors.New("沒有內建的 migration"))
	}
	return versions[len(versions)-1], nil
}

// Migrations 回傳要交給 server 的 migration（app.TestControls.Migrations）；沒有任何覆寫時回傳 nil，用內建的。
// `the current schema version is N` 只提供版本 1 到 N；`migration N fails` 把第 N 支換成會失敗的。
func (w *World) Migrations() (fs.FS, error) {
	if w.CurrentSchema == 0 && w.FailMigration == 0 {
		return nil, nil
	}
	top := w.CurrentSchema
	if top == 0 {
		latest, err := latestBuiltin()
		if err != nil {
			return nil, err
		}
		top = max(latest, w.FailMigration)
	}
	if w.FailMigration > top {
		return nil, fmt.Errorf("migration %d 超過目前的 schema 版本 %d", w.FailMigration, top)
	}
	return migrationsUpTo(top, w.FailMigration)
}

// openRW 開啟路徑指定的資料庫（可寫），用於建立與比對參考 schema；journal 用預設的，不改變檔案模式。
func openRW(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"_pragma": {"busy_timeout(5000)"}}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// migrateTo 在 path 建立資料庫並套用 migration 到 version。
func migrateTo(ctx context.Context, path string, version int) error {
	migs, err := migrationsUpTo(version, 0)
	if err != nil {
		return err
	}
	db, err := openRW(path)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := goose.NewProvider(goose.DialectSQLite3, db, migs)
	if err != nil {
		return err
	}
	if _, err := p.UpTo(ctx, int64(version)); err != nil {
		return err
	}
	return nil
}

// CreateDBAtSchema 以 schema 版本 version 建立 /data 的資料庫（內建 migration 加 goose 的 UpTo；
// version 比內建的最新版本大時補合成的 migration）。資料庫已存在時回傳錯誤。
func (w *World) CreateDBAtSchema(ctx context.Context, version int) error {
	if version < 1 {
		return fmt.Errorf("schema 版本 %d 不正確", version)
	}
	path := filepath.Join(w.DataDir(), store.DBFileName)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("資料庫 %s 已經存在", w.Unexpand(path))
	}
	if err := migrateTo(ctx, path, version); err != nil {
		return fmt.Errorf("建立 schema 版本 %d 的資料庫：%w", version, err)
	}
	return os.Chmod(path, 0o600)
}

// schemaObjects 讀出資料庫的 schema（sqlite_master，不含 SQLite 內部的表），每個物件一行，排序後回傳。
func schemaObjects(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, name, tbl, stmt string
		if err := rows.Scan(&typ, &name, &tbl, &stmt); err != nil {
			return nil, err
		}
		out = append(out, fmt.Sprintf("%s %s on %s: %s", typ, name, tbl, strings.Join(strings.Fields(stmt), " ")))
	}
	return out, rows.Err()
}

// gooseVersion 讀出 goose 版本表中已套用的最新版本；沒有版本表時為 0。
func gooseVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&v)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return 0, nil
		}
		return 0, err
	}
	return int(v.Int64), nil
}

// CheckDBSchema 確認資料庫目前的 schema 版本就是 version：goose 版本表的最新版本是 version，
// 而且 sqlite_master 與「全新資料庫跑到 version」的參考 schema 完全相同（所以失敗的 migration 留下的 marker 表
// 也會被發現）。以唯讀方式查詢，不經過 production 程式。
func (w *World) CheckDBSchema(ctx context.Context, version int) error {
	var got []string
	var gotVersion int
	err := w.dbQuery(func(ctx context.Context, db *sql.DB) (err error) {
		if gotVersion, err = gooseVersion(ctx, db); err != nil {
			return err
		}
		got, err = schemaObjects(ctx, db)
		return err
	})
	if err != nil {
		return fmt.Errorf("讀取資料庫的 schema：%w", err)
	}
	if gotVersion != version {
		return fmt.Errorf("資料庫的 schema 版本是 %d，預期 %d", gotVersion, version)
	}

	dir, err := os.MkdirTemp("", "nstschema")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	ref := filepath.Join(dir, "ref.db")
	if err := migrateTo(ctx, ref, version); err != nil {
		return fmt.Errorf("建立 schema 版本 %d 的參考資料庫：%w", version, err)
	}
	refDB, err := openRW(ref)
	if err != nil {
		return err
	}
	defer refDB.Close()
	want, err := schemaObjects(ctx, refDB)
	if err != nil {
		return fmt.Errorf("讀取參考 schema：%w", err)
	}
	if !slices.Equal(got, want) {
		var extra, missing []string
		for _, g := range got {
			if !slices.Contains(want, g) {
				extra = append(extra, g)
			}
		}
		for _, x := range want {
			if !slices.Contains(got, x) {
				missing = append(missing, x)
			}
		}
		return fmt.Errorf("資料庫的 schema 與版本 %d 的參考 schema 不同\n多出：%q\n缺少：%q", version, extra, missing)
	}
	return nil
}

// DBItemRefs 唯讀地列出資料庫中的影音代號（不含垃圾桶中的）。
func (w *World) DBItemRefs() ([]string, error) {
	var refs []string
	err := w.dbQuery(func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, `SELECT platform, external_id, part FROM items WHERE deleted_at IS NULL ORDER BY created_at, rowid`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var platform, ext string
			var part int
			if err := rows.Scan(&platform, &ext, &part); err != nil {
				return err
			}
			refs = append(refs, source.Ref(platform, ext, part))
		}
		return rows.Err()
	})
	return refs, err
}
