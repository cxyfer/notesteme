package app

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// NFR-SEC-5：資料目錄 0700、DB 與 WAL／SHM 0600，與 umask 無關，已存在的目錄也要收緊。
func TestStartServerEnforcesPrivateModes(t *testing.T) {
	// umask 是整個 process 共用的，這個測試不可以平行執行。
	old := syscall.Umask(0o022)
	t.Cleanup(func() { syscall.Umask(old) })

	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	// 資料目錄事先以寬鬆的權限存在。
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(data, 0o755); err != nil {
		t.Fatal(err)
	}

	srv, err := StartServer(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	// 寫入一筆資料，確保 WAL 與 SHM 都已建立。
	if err := srv.store.SetSetting(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}

	check := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			return
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %04o, want %04o", path, got, want)
		}
	}
	check(data, 0o700)
	check(filepath.Join(data, "tmp"), 0o700)
	for _, name := range []string{"notesteme.db", "notesteme.db-wal", "notesteme.db-shm"} {
		check(filepath.Join(data, name), 0o600)
	}
}
