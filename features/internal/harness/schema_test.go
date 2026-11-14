package harness

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

func TestMigrationsOverrides(t *testing.T) {
	w := NewT(t)
	if m, err := w.Migrations(); err != nil || m != nil {
		t.Fatalf("沒有覆寫時應該用內建的：%v, %v", m, err)
	}
	latest, err := latestBuiltin()
	if err != nil || latest < 5 {
		t.Fatalf("latestBuiltin = %d, %v", latest, err)
	}

	count := func(fsys fs.FS) int {
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}
	w.CurrentSchema = 3
	m, err := w.Migrations()
	if err != nil || count(m) != 3 {
		t.Fatalf("current=3：%d 支，%v", count(m), err)
	}

	// 超過內建的最新版本：補合成的 migration。
	w.CurrentSchema = latest + 2
	m, err = w.Migrations()
	if err != nil || count(m) != int(latest)+2 {
		t.Fatalf("current=latest+2：%d 支，%v", count(m), err)
	}

	// migration N 換成會失敗的那一支：NO TRANSACTION，先建立 marker 表。
	w.CurrentSchema, w.FailMigration = 4, 4
	m, err = w.Migrations()
	if err != nil || count(m) != 4 {
		t.Fatal(err)
	}
	b, err := fs.ReadFile(m, "00004_fails.sql")
	if err != nil || !strings.Contains(string(b), "-- +goose NO TRANSACTION") || !strings.Contains(string(b), "migration_4_marker") {
		t.Fatalf("失敗的 migration = %q, %v", b, err)
	}
	if _, err := fs.ReadFile(m, "00004_secrets_resume.sql"); err == nil {
		t.Error("第 4 支應該被換掉")
	}

	// 只指定 fail：版本取內建的最新；超出現行版本是錯誤。
	w.CurrentSchema, w.FailMigration = 0, 2
	if m, err = w.Migrations(); err != nil || count(m) != int(latest) {
		t.Errorf("只有 fail=2：%d 支，%v", count(m), err)
	}
	w.CurrentSchema, w.FailMigration = 3, 4
	if _, err := w.Migrations(); err == nil {
		t.Error("migration 4 超過現行的 schema 版本 3，應該回傳錯誤")
	}
}

func TestCreateDBAtSchemaAndCheck(t *testing.T) {
	ctx := context.Background()
	w := NewT(t)
	if err := w.CreateDBAtSchema(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := w.CreateDBAtSchema(ctx, 3); err == nil {
		t.Error("資料庫已存在時應該失敗")
	}
	if err := w.CheckDBSchema(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckDBSchema(ctx, 4); err == nil || !strings.Contains(err.Error(), "版本是 3") {
		t.Errorf("版本不符的錯誤 = %v", err)
	}
	path := filepath.Join(w.DataDir(), store.DBFileName)
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("權限 = %o", fi.Mode().Perm())
	}

	// 版本相同，但 schema 多了失敗 migration 留下的 marker 表：要被發現。
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE migration_4_marker (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	err = w.CheckDBSchema(ctx, 3)
	if err == nil || !strings.Contains(err.Error(), "migration_4_marker") {
		t.Errorf("marker 表應該被發現：%v", err)
	}
}

// 超過內建最新版本：補合成的 migration，版本與 schema 都對得上。
func TestCreateDBAtSyntheticSchema(t *testing.T) {
	ctx := context.Background()
	w := NewT(t)
	latest, _ := latestBuiltin()
	if err := w.CreateDBAtSchema(ctx, int(latest)+1); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckDBSchema(ctx, int(latest)+1); err != nil {
		t.Fatal(err)
	}
	if err := w.CheckDBSchema(ctx, int(latest)); err == nil {
		t.Error("版本不符應該失敗")
	}
	if err := w.CreateDBAtSchema(ctx, 0); err == nil {
		t.Error("版本 0 不正確")
	}
}

func TestDBItemRefs(t *testing.T) {
	ctx := context.Background()
	w := NewT(t)
	if err := w.CreateDBAtSchema(ctx, 5); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(w.DataDir(), store.DBFileName)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO items (id, platform, external_id, part, url, created_at) VALUES ('a', 'youtube', 'abc123xxxxx', 0, 'u', 1)`,
		`INSERT INTO items (id, platform, external_id, part, url, created_at, deleted_at) VALUES ('b', 'youtube', 'trash01xxxx', 0, 'u', 2, 5)`,
		`INSERT INTO items (id, platform, external_id, part, url, created_at) VALUES ('c', 'bilibili', 'BV1xx', 2, 'u', 3)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()
	refs, err := w.DBItemRefs()
	if err != nil || len(refs) != 2 || refs[0] != "yt:abc123xxxxx" || refs[1] != "bili:BV1xx/p2" {
		t.Errorf("DBItemRefs = %v, %v；預期不含垃圾桶中的影音", refs, err)
	}
}

func TestBackupsVirtualPath(t *testing.T) {
	w := NewT(t)
	if got := w.Expand("/backups/x.tar.gz"); got != filepath.Join(w.Root, "backups", "x.tar.gz") {
		t.Errorf("Expand = %q", got)
	}
	// /data/backups 屬於 /data，不會被 /backups 的規則改寫。
	if got := w.Expand("/data/backups/pre-migration-3.db"); got != filepath.Join(w.Root, "data", "backups", "pre-migration-3.db") {
		t.Errorf("Expand /data/backups = %q", got)
	}
	if got := w.Unexpand(filepath.Join(w.Root, "backups", "x")) + " " + w.Unexpand(filepath.Join(w.Root, "data", "backups", "y")); got != "/backups/x /data/backups/y" {
		t.Errorf("Unexpand = %q", got)
	}
}
