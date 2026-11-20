package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// migrationsUpTo 回傳內建 migration 的前 n 支；replace 的版本換成指定的內容。
func migrationsUpTo(t *testing.T, n int, replace map[int]string) fs.FS {
	t.Helper()
	all, err := fs.ReadDir(MigrationsFS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for i, e := range all {
		v := i + 1
		if v > n {
			break
		}
		b, err := fs.ReadFile(MigrationsFS(), e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if r, ok := replace[v]; ok {
			b = []byte(r)
		}
		out[e.Name()] = &fstest.MapFile{Data: b}
	}
	return out
}

func openWith(t *testing.T, dir string, migs fs.FS) (*Store, error) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC))
	return Open(context.Background(), dir, clk, Options{Migrations: migs})
}

// 新的資料庫（版本 0）升級時不留快照。
func TestMigrateNewDatabaseHasNoSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := openWith(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := os.Stat(filepath.Join(dir, BackupsDirName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("新資料庫不該有 backups 目錄：%v", err)
	}
}

// 有待執行的 migration 時先留快照：檔名帶舊版本、權限 0600、沒有殘留的暫存檔，內容是升級前的版本。
func TestMigrateTakesSnapshotBeforeUpgrade(t *testing.T) {
	dir := t.TempDir()
	s, err := openWith(t, dir, migrationsUpTo(t, 3, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = openWith(t, dir, migrationsUpTo(t, 5, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, err := s.SchemaVersion(t.Context()); err != nil || v != 5 {
		t.Fatalf("升級後的版本 = %d, %v；預期 5", v, err)
	}

	snap := filepath.Join(dir, BackupsDirName, "pre-migration-3.db")
	fi, err := os.Stat(snap)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("快照的權限 = %o，預期 0600", fi.Mode().Perm())
	}
	if v, err := VerifyFile(t.Context(), snap); err != nil || v != 3 {
		t.Errorf("快照的版本 = %d, %v；預期 3", v, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, BackupsDirName))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("殘留暫存檔 %s", e.Name())
		}
	}
}

// 沒有待執行的 migration 時不留快照。
func TestMigrateUpToDateHasNoSnapshot(t *testing.T) {
	dir := t.TempDir()
	for range 2 {
		s, err := openWith(t, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, BackupsDirName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("沒有待執行的 migration 不該留快照：%v", err)
	}
}

const failingMigrationSQL = `-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE marker (id INTEGER);
INSERT INTO no_such_table VALUES (1);
-- +goose Down
DROP TABLE marker;
`

// migration 失敗：還原快照（marker 表不留下）、錯誤說明是哪一支、原因與已還原的快照，連線與 -wal 都乾淨。
func TestMigrateFailureRestoresSnapshot(t *testing.T) {
	dir := t.TempDir()
	s, err := openWith(t, dir, migrationsUpTo(t, 3, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err = openWith(t, dir, migrationsUpTo(t, 4, map[int]string{4: failingMigrationSQL}))
	var me *MigrationError
	if !errors.As(err, &me) {
		t.Fatalf("錯誤 = %v，預期 *MigrationError", err)
	}
	snap := filepath.Join(dir, BackupsDirName, "pre-migration-3.db")
	if me.Version != 4 || me.Snapshot != snap || me.RestoreErr != nil {
		t.Fatalf("MigrationError = %+v", me)
	}
	for _, want := range []string{"migration 4 失敗", "no such table", "已還原 " + snap} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("訊息 %q 不含 %q", err, want)
		}
	}

	path := filepath.Join(dir, DBFileName)
	if v, err := VerifyFile(t.Context(), path); err != nil || v != 3 {
		t.Fatalf("還原後的版本 = %d, %v；預期 3", v, err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'marker'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("marker 表還在（%d, %v）：失敗的 migration 沒有被還原", n, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s 還在：%v", suffix, err)
		}
	}
}

// 新資料庫的 migration 失敗：沒有快照可以還原，錯誤照樣回報，不宣稱已還原。
func TestMigrateFailureOnNewDatabase(t *testing.T) {
	_, err := openWith(t, t.TempDir(), migrationsUpTo(t, 1, map[int]string{1: failingMigrationSQL}))
	var me *MigrationError
	if !errors.As(err, &me) || me.Snapshot != "" {
		t.Fatalf("錯誤 = %v", err)
	}
	if strings.Contains(err.Error(), "已還原") {
		t.Errorf("沒有快照卻說已還原：%v", err)
	}
}

// 資料庫比程式新：拒絕、說明兩個版本與快照的位置，而且不動資料庫。
func TestMigrateRefusesNewerDatabase(t *testing.T) {
	dir := t.TempDir()
	s, err := openWith(t, dir, migrationsUpTo(t, 5, nil))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	_, err = openWith(t, dir, migrationsUpTo(t, 4, nil))
	var tooNew *SchemaTooNewError
	if !errors.As(err, &tooNew) || tooNew.DB != 5 || tooNew.Program != 4 {
		t.Fatalf("錯誤 = %v，預期 SchemaTooNewError（DB 5、程式 4）", err)
	}
	for _, want := range []string{"schema 版本 5", "支援的 4", "不支援降級", "pre-migration-*.db"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("訊息 %q 不含 %q", err, want)
		}
	}
	if v, err := VerifyFile(t.Context(), filepath.Join(dir, DBFileName)); err != nil || v != 5 {
		t.Errorf("資料庫的版本 = %d, %v；預期維持 5", v, err)
	}
}

// 升級到 00006 時，既有標題的 title_fold 用 Go 的 FoldTitle 回填，非 ASCII 的大小寫也要轉換（LIB-R3）。
func TestMigrationBackfillsTitleFoldWithGoFold(t *testing.T) {
	dir := t.TempDir()
	s, err := openWith(t, dir, migrationsUpTo(t, 5, nil))
	if err != nil {
		t.Fatal(err)
	}
	const title = "ÉCOLE Straße ΣΊΣΥΦΟΣ"
	if _, err := s.w.ExecContext(t.Context(),
		`INSERT INTO items (id, platform, external_id, url, title, created_at) VALUES ('a', 'youtube', 'x', 'https://example.com', ?, 1)`, title); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s, err = openWith(t, dir, migrationsUpTo(t, 6, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var fold string
	if err := s.r.QueryRowContext(t.Context(), `SELECT title_fold FROM items WHERE id = 'a'`).Scan(&fold); err != nil {
		t.Fatal(err)
	}
	if want := FoldTitle(title); fold != want {
		t.Errorf("title_fold = %q，預期 %q", fold, want)
	}
}
