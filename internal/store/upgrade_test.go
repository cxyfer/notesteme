package store_test

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/cxyfer/notesteme/internal/benchdata"
	"github.com/cxyfer/notesteme/internal/sqlitedb"
	"github.com/cxyfer/notesteme/internal/store"
)

// NFR-CMP-5「在 CI 驗證從上一個發布版本升級」：testdata/upgrade/schema-<版本>/ 是某個 schema 版本的資料目錄
// （DB 加上字幕產出檔，由 internal/benchdata 產生）。每份 fixture 都複製到暫存目錄後以 store.Open 升級到最新版本，
// 檢查資料還在、全文搜尋索引完整。fixture 一旦提交就不再修改；新增方式見 testdata/upgrade/README.md。
var updateUpgradeFixture = flag.Bool("update-upgrade-fixture", false, "以目前的 schema 版本新增 testdata/upgrade/schema-<版本>/")

// upgradeProfile 是 fixture 的內容：3 部影音（2 部英文、1 部中文），每部 3 個字幕版本、各 40 句。
func upgradeProfile() benchdata.Profile {
	p := benchdata.Lib10k
	p.Name, p.Items, p.Channels, p.CuesPerItem = "upgrade-fixture", 3, 2, 40
	return p
}

func latestSchema(t *testing.T) int64 {
	t.Helper()
	entries, err := fs.ReadDir(store.MigrationsFS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var latest int64
	re := regexp.MustCompile(`^(\d+)_.*\.sql$`)
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil {
			v, _ := strconv.ParseInt(m[1], 10, 64)
			latest = max(latest, v)
		}
	}
	return latest
}

func TestUpgradeFromReleasedSchemas(t *testing.T) {
	ctx := context.Background()
	latest := latestSchema(t)
	if *updateUpgradeFixture {
		dst := filepath.Join("testdata", "upgrade", fmt.Sprintf("schema-%d", latest))
		if _, err := os.Stat(dst); err == nil {
			t.Fatalf("%s 已經存在；fixture 提交後不再修改", dst)
		}
		if _, err := benchdata.Generate(ctx, dst, upgradeProfile(), benchdata.Options{}); err != nil {
			t.Fatal(err)
		}
		abs, err := filepath.Abs(filepath.Join(dst, store.DBFileName)) // file: URI 不接受相對路徑
		if err != nil {
			t.Fatal(err)
		}
		db, err := sqlitedb.Open(abs, false)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.ExecContext(ctx, `VACUUM`)
		if cerr := db.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			_ = os.Remove(filepath.Join(dst, store.DBFileName+suffix))
		}
		_ = os.RemoveAll(filepath.Join(dst, store.BackupsDirName))
	}

	fixtures, err := filepath.Glob(filepath.Join("testdata", "upgrade", "schema-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("沒有任何升級 fixture（testdata/upgrade/schema-*）")
	}
	for _, src := range fixtures {
		t.Run(filepath.Base(src), func(t *testing.T) {
			from, err := strconv.ParseInt(filepath.Base(src)[len("schema-"):], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(ctx, dir, benchdata.DefaultClock())
			if err != nil {
				t.Fatalf("從 schema %d 升級失敗：%v", from, err)
			}
			defer st.Close()
			if from < latest {
				// OPS-R3：有待執行的 migration 時先留快照。
				if _, err := os.Stat(filepath.Join(dir, store.BackupsDirName, store.PreMigrationName(from))); err != nil {
					t.Errorf("升級前沒有留下快照：%v", err)
				}
			}
			page, err := st.Read().ListItems(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) != upgradeProfile().Items {
				t.Fatalf("升級後有 %d 部影音，預期 %d", len(page), upgradeProfile().Items)
			}
			for _, it := range page {
				n, err := st.SearchIndexCheck(ctx, it.ID)
				if err != nil || n != upgradeProfile().CuesPerItem {
					t.Errorf("%s 的索引 = %d 句（%v），預期 %d", it.Title, n, err, upgradeProfile().CuesPerItem)
				}
				cur, err := st.Read().CurrentArtifacts(ctx, it.ID, "transcript")
				if err != nil || len(cur) == 0 {
					t.Errorf("%s 沒有現行的字幕：%v", it.Title, err)
				}
			}
		})
	}
}
