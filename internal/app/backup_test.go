package app

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cxyfer/notesteme/internal/backup"
	"github.com/cxyfer/notesteme/internal/store"
)

const backupMasterKey = "backup-test-master-key-9f3a"

// startBackupServer 啟動 serve，並設定一個可辨識的 master key。
func startBackupServer(t *testing.T, env Env) *Server {
	t.Helper()
	env.Vars["NOTESTEME_MASTER_KEY"] = backupMasterKey
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Kill)
	return srv
}

// Begin 產生的備份：檔名與 manifest 取自 clock、內容可以被讀回、不含 master key，Close 清掉暫存的快照。
func TestBackupSourceBeginAndWrite(t *testing.T) {
	env := testEnv(t)
	if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], []byte("listen: 127.0.0.1:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startBackupServer(t, env)
	if err := srv.store.SetSetting(t.Context(), "marker", "before-backup"); err != nil {
		t.Fatal(err)
	}

	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := "notesteme-backup-20261112T100000Z.tar.gz"; b.Name() != want {
		t.Errorf("Name = %q，預期 %q", b.Name(), want)
	}
	// 快照之後的寫入不影響已經準備好的備份。
	if err := srv.store.SetSetting(t.Context(), "marker", "after-backup"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}

	work := t.TempDir()
	c, err := backup.Extract(bytes.NewReader(buf.Bytes()), work, backup.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	latest, _ := store.LatestSchemaVersion(nil)
	if m := c.Manifest; m.SchemaVersion != latest || m.NotestemeVersion != version || !m.CreatedAt.Equal(time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("manifest = %+v", m)
	}
	if !c.HasConfig || string(c.Config) != "listen: 127.0.0.1:9\n" {
		t.Errorf("設定檔 = %v %q", c.HasConfig, c.Config)
	}
	if v, err := store.VerifyFile(t.Context(), c.DBPath); err != nil || v != latest {
		t.Errorf("備份中的資料庫 = %d, %v", v, err)
	}
	if raw, _ := os.ReadFile(c.DBPath); !bytes.Contains(raw, []byte("before-backup")) || bytes.Contains(raw, []byte("after-backup")) {
		t.Error("備份的資料庫應該是 Begin 當下的內容")
	}
	// master key 不在備份中：DB 裡只有用它加密過的資料金鑰。
	if bytes.Contains(buf.Bytes(), []byte(backupMasterKey)) {
		t.Error("備份（壓縮後）含有 master key")
	}
	if raw, _ := os.ReadFile(c.DBPath); bytes.Contains(raw, []byte(backupMasterKey)) {
		t.Error("備份的資料庫含有 master key")
	}

	tmp, _ := filepath.Glob(filepath.Join(srv.cfg.Data, "tmp", "backup-*"))
	if len(tmp) != 1 {
		t.Fatalf("Close 之前應該有一個暫存目錄：%v", tmp)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp[0]); !os.IsNotExist(err) {
		t.Errorf("Close 之後暫存目錄還在：%v", err)
	}
}

// 沒有設定檔時備份不放設定檔（OPS-R1：有這個檔案才放）。
func TestBackupSourceWithoutConfigFile(t *testing.T) {
	env := testEnv(t)
	srv := startBackupServer(t, env)
	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := backup.Extract(&buf, t.TempDir(), backup.Limits{})
	if err != nil || c.HasConfig {
		t.Fatalf("Extract = %+v, %v；預期沒有設定檔", c, err)
	}
}

// 設定檔太大：拒絕，並清掉已經建立的暫存快照。
func TestBackupSourceConfigTooLarge(t *testing.T) {
	env := testEnv(t)
	big := bytes.Repeat([]byte("#"), maxConfigFileBytes+1)
	if err := os.WriteFile(env.Vars["NOTESTEME_CONFIG"], big, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := startBackupServer(t, env)
	if _, err := srv.backupSource(env).Begin(t.Context()); err == nil {
		t.Fatal("設定檔超過上限應該失敗")
	}
	if tmp, _ := filepath.Glob(filepath.Join(srv.cfg.Data, "tmp", "backup-*")); len(tmp) != 0 {
		t.Errorf("失敗後不該留下暫存目錄：%v", tmp)
	}
}

// 備份中的 schema 版本是資料庫實際的版本（TestControls.Migrations 模擬較舊的程式）。
func TestBackupManifestSchemaVersion(t *testing.T) {
	env := testEnv(t)
	custom := fstest.MapFS{}
	for _, name := range []string{"00001_init.sql", "00002_encryption_keys.sql", "00003_items_pipeline.sql"} {
		b, err := fs.ReadFile(store.MigrationsFS(), name)
		if err != nil {
			t.Fatal(err)
		}
		custom[name] = &fstest.MapFile{Data: b}
	}
	env.Test.Migrations = custom
	srv := startBackupServer(t, env)
	b, err := srv.backupSource(env).Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var buf bytes.Buffer
	if _, err := b.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	c, err := backup.Extract(&buf, t.TempDir(), backup.Limits{})
	if err != nil || c.Manifest.SchemaVersion != 3 {
		t.Fatalf("manifest = %+v, %v；預期 schema 3", c, err)
	}
	if !strings.HasPrefix(b.Name(), "notesteme-backup-") {
		t.Errorf("Name = %q", b.Name())
	}
}
