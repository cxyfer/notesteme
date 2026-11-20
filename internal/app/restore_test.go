package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/store"
)

// makeBackup 啟動 serve、寫入 marker 設定、備份成檔案後正常關閉；回傳備份檔路徑。
func makeBackup(t *testing.T, env Env, marker string) string {
	t.Helper()
	srv := startBackupServer(t, env)
	if err := srv.store.SetSetting(t.Context(), "marker", marker); err != nil {
		t.Fatal(err)
	}
	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), b.Name())
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_ = b.Close()
	if err := srv.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	return path
}

// marker 以 store 開啟資料目錄，讀出 marker 設定的值。
func marker(t *testing.T, env Env) string {
	t.Helper()
	st, err := store.Open(t.Context(), env.Vars["NOTESTEME_DATA"], env.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	v, _, err := st.GetSetting(t.Context(), "marker")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func setMarker(t *testing.T, env Env, v string) {
	t.Helper()
	if err := os.MkdirAll(env.Vars["NOTESTEME_DATA"], 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), env.Vars["NOTESTEME_DATA"], env.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.SetSetting(t.Context(), "marker", v); err != nil {
		t.Fatal(err)
	}
}

// 完整的還原：資料庫換成備份的內容、還原前的資料庫留成 pre-restore、-wal 與 -shm 被刪除、
// 設定檔解到 backups/restored-config-<時間>/，不碰原本的設定檔。
func TestRestoreReplacesDatabase(t *testing.T) {
	env := testEnv(t)
	cfgPath := env.Vars["NOTESTEME_CONFIG"]
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := makeBackup(t, env, "from-backup")
	setMarker(t, env, "newer-than-backup")

	// 遺留的 -wal、-shm 屬於被換掉的資料庫。
	data := env.Vars["NOTESTEME_DATA"]
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(filepath.Join(data, store.DBFileName+suffix), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(cfgPath, []byte("listen: 127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Clock.(*clock.Fake).Advance(time.Hour)

	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(filepath.Join(data, store.DBFileName+suffix)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s 還在：%v", suffix, err)
		}
	}
	if got := marker(t, env); got != "from-backup" {
		t.Errorf("還原後的 marker = %q，預期 from-backup", got)
	}

	wantPre := filepath.Join(data, "backups", "pre-restore-20261112T110000Z.db")
	if res.PreRestore != wantPre {
		t.Errorf("PreRestore = %q，預期 %q", res.PreRestore, wantPre)
	}
	if fi, err := os.Stat(wantPre); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("pre-restore：%v, %v", fi, err)
	}
	if v, err := store.VerifyFile(t.Context(), wantPre); err != nil || v == 0 {
		t.Errorf("pre-restore 不是有效的資料庫：%d, %v", v, err)
	}

	wantCfg := filepath.Join(data, "backups", "restored-config-20261112T110000Z")
	if res.ConfigDir != wantCfg {
		t.Errorf("ConfigDir = %q，預期 %q", res.ConfigDir, wantCfg)
	}
	if b, _ := os.ReadFile(filepath.Join(wantCfg, "notesteme.yaml")); string(b) != "listen: 127.0.0.1:9\n" {
		t.Errorf("解出的設定檔 = %q", b)
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != "listen: 127.0.0.1:1\n" {
		t.Errorf("原本的設定檔被覆寫：%q", b)
	}
	if latest, _ := store.LatestSchemaVersion(nil); res.SchemaVersion != latest {
		t.Errorf("SchemaVersion = %d", res.SchemaVersion)
	}
	// 鎖已釋放：serve 可以接著啟動，而且看到還原的資料。
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatalf("還原後 serve 無法啟動：%v", err)
	}
	srv.Kill()
}

// pre-restore 是還原前資料庫的內容。
func TestRestoreKeepsPreRestoreSnapshot(t *testing.T) {
	env := testEnv(t)
	file := makeBackup(t, env, "from-backup")
	setMarker(t, env, "current")
	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	if res.ConfigDir != "" {
		t.Errorf("備份沒有設定檔，ConfigDir = %q", res.ConfigDir)
	}
	dir := t.TempDir()
	if err := os.Rename(res.PreRestore, filepath.Join(dir, store.DBFileName)); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(t.Context(), dir, env.Clock)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _, _ := st.GetSetting(t.Context(), "marker"); v != "current" {
		t.Errorf("pre-restore 的 marker = %q，預期 current", v)
	}
}

// 全新的資料目錄（沒有資料庫）也能還原，沒有 pre-restore。
func TestRestoreIntoEmptyDataDir(t *testing.T) {
	src := testEnv(t)
	file := makeBackup(t, src, "from-backup")
	env := testEnv(t)
	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	if res.PreRestore != "" {
		t.Errorf("PreRestore = %q，預期沒有", res.PreRestore)
	}
	if got := marker(t, env); got != "from-backup" {
		t.Errorf("marker = %q", got)
	}
}

// 現有的資料庫已經損毀（常見的還原理由）：改複製原始檔案當作 pre-restore，還原照常完成。
func TestRestoreWithCorruptCurrentDatabase(t *testing.T) {
	env := testEnv(t)
	file := makeBackup(t, env, "from-backup")
	data := env.Vars["NOTESTEME_DATA"]
	garbage := []byte(strings.Repeat("garbage", 1000))
	if err := os.WriteFile(filepath.Join(data, store.DBFileName), garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(res.PreRestore); string(b) != string(garbage) {
		t.Error("損毀的資料庫應該原樣留成 pre-restore")
	}
	if got := marker(t, env); got != "from-backup" {
		t.Errorf("marker = %q", got)
	}
}

// serve 持有資料目錄：回 ErrServeRunning，而且先檢查鎖、不讀備份檔（檔案不存在也是這個錯誤）、不動資料。
func TestRestoreRefusedWhileServing(t *testing.T) {
	env := testEnv(t)
	srv := startBackupServer(t, env)
	if err := srv.store.SetSetting(t.Context(), "marker", "live"); err != nil {
		t.Fatal(err)
	}
	_, err := Restore(t.Context(), env, filepath.Join(t.TempDir(), "does-not-exist.tar.gz"))
	if !errors.Is(err, ErrServeRunning) || !strings.Contains(err.Error(), "請先停止 notesteme serve") {
		t.Fatalf("錯誤 = %v，預期 ErrServeRunning", err)
	}
	if v, _, _ := srv.store.GetSetting(t.Context(), "marker"); v != "live" {
		t.Errorf("資料被動到：%q", v)
	}
	if _, err := os.Stat(filepath.Join(env.Vars["NOTESTEME_DATA"], "backups")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("被拒絕時不該建立 backups：%v", err)
	}
}

// 資料目錄被另一個 CLI 持有：說明是哪個程序。
func TestRestoreRefusedWhileCLIHoldsLock(t *testing.T) {
	env := testEnv(t)
	sa, err := OpenStandalone(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	_, err = Restore(t.Context(), env, "x")
	var byCLI *lockedByCLIError
	if !errors.As(err, &byCLI) || errors.Is(err, ErrServeRunning) {
		t.Fatalf("錯誤 = %v，預期 lockedByCLIError", err)
	}
}

// writeArchive 以 manifest 的 schema 版本與真實的資料庫快照組出備份檔。
func writeArchive(t *testing.T, env Env, manifestVersion int64) string {
	t.Helper()
	src := makeBackup(t, env, "x")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	work := t.TempDir()
	c, err := backup.Extract(in, work, backup.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	c.Manifest.SchemaVersion = manifestVersion
	a := &backup.Archive{Manifest: c.Manifest, DBPath: c.DBPath}
	path := filepath.Join(t.TempDir(), backup.FileName(time.Now()))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

// 備份的 schema 版本比程式新：拒絕，資料庫不動、不留下 pre-restore。
func TestRestoreRefusesNewerSchema(t *testing.T) {
	env := testEnv(t)
	latest, _ := store.LatestSchemaVersion(nil)
	file := writeArchive(t, env, latest+1)
	setMarker(t, env, "current")

	_, err := Restore(t.Context(), env, file)
	if err == nil || !strings.Contains(err.Error(), "不支援還原") {
		t.Fatalf("錯誤 = %v", err)
	}
	if got := marker(t, env); got != "current" {
		t.Errorf("被拒絕後資料庫被動到：%q", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(env.Vars["NOTESTEME_DATA"], "backups")); len(entries) != 0 {
		t.Errorf("被拒絕時不該留下檔案：%v", entries)
	}
}

// 備份的資料庫實際版本比程式新，即使 manifest 寫得比較舊：用資料庫本身的版本判斷。
func TestRestoreRefusesDatabaseNewerThanManifest(t *testing.T) {
	env := testEnv(t)
	file := writeArchive(t, env, 3) // manifest 說 3，資料庫其實是內建的最新版本
	// 假裝程式比資料庫舊：只提供前 4 支。
	env.Test.Migrations = fsOf(t, 4)
	_, err := Restore(t.Context(), env, file)
	if err == nil || !strings.Contains(err.Error(), "不支援還原") {
		t.Fatalf("錯誤 = %v", err)
	}
}

// manifest 與資料庫的版本不一致：視為損毀的備份。
func TestRestoreRefusesVersionMismatch(t *testing.T) {
	env := testEnv(t)
	file := writeArchive(t, env, 3)
	_, err := Restore(t.Context(), env, file)
	if !errors.Is(err, backup.ErrBadArchive) || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("錯誤 = %v", err)
	}
}

// 不安全或損毀的備份：拒絕，資料庫不動。
func TestRestoreRefusesBadArchives(t *testing.T) {
	env := testEnv(t)
	setMarker(t, env, "current")
	dir := t.TempDir()
	for name, content := range map[string]string{
		"not-gzip.tar.gz": "fixture: backup-schema-3",
		"empty.tar.gz":    "",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Restore(t.Context(), env, path); !errors.Is(err, backup.ErrBadArchive) {
			t.Errorf("%s：錯誤 = %v", name, err)
		}
	}
	if _, err := Restore(t.Context(), env, filepath.Join(dir, "missing.tar.gz")); err == nil {
		t.Error("備份檔不存在應該失敗")
	}
	if got := marker(t, env); got != "current" {
		t.Errorf("資料庫被動到：%q", got)
	}
	// 失敗之後鎖已釋放、解開的暫存目錄清掉。
	if tmp, _ := filepath.Glob(filepath.Join(env.Vars["NOTESTEME_DATA"], "tmp", "restore-*")); len(tmp) != 0 {
		t.Errorf("留下暫存目錄：%v", tmp)
	}
	l, err := store.LockDir(env.Vars["NOTESTEME_DATA"])
	if err != nil {
		t.Fatalf("鎖沒有釋放：%v", err)
	}
	_ = l.Release()
}

// fsOf 回傳內建 migration 的前 n 支。
func fsOf(t *testing.T, n int) fs.FS {
	t.Helper()
	all, err := fs.ReadDir(store.MigrationsFS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, e := range all[:n] {
		b, err := fs.ReadFile(store.MigrationsFS(), e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = &fstest.MapFile{Data: b}
	}
	return out
}

func TestRestoreNeedsClock(t *testing.T) {
	env := testEnv(t)
	env.Clock = nil
	if _, err := Restore(context.Background(), env, "x"); err == nil {
		t.Error("沒有 Clock 應該失敗")
	}
}

// 設定檔寫不出來時，還原要在換上資料庫之前失敗：回報失敗時，資料庫不能已經被換掉。
func TestRestoreFailsBeforeReplacingDatabaseWhenConfigCannotBeWritten(t *testing.T) {
	src := testEnv(t)
	if err := os.WriteFile(src.Vars["NOTESTEME_CONFIG"], []byte("listen: 127.0.0.1:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := makeBackup(t, src, "from-backup")

	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	backups := filepath.Join(data, store.BackupsDirName)
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(backups, 0o500); err != nil { // 唯讀：建不了 restored-config-*
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(backups, 0o700) })

	if _, err := Restore(t.Context(), env, file); err == nil {
		t.Fatal("設定檔寫不出來，Restore 應該失敗")
	}
	if _, err := os.Stat(filepath.Join(data, store.DBFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("回報失敗時資料庫不該已經換上：%v", err)
	}
}
