package app

import (
	"errors"
	"os"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// 資料目錄已被別的程序持有時，standalone 回報 ErrLocked，不會先抱怨沒有 master key，
// 也不會改動既有目錄的權限（呼叫端要改走 client 模式，client 不需要 master key）。
func TestStandaloneWithoutMasterKeyWhileLocked(t *testing.T) {
	env := testEnv(t)
	srv, err := StartServer(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Kill()
	data := env.Vars["NOTESTEME_DATA"]
	if err := os.Chmod(data, 0o750); err != nil {
		t.Fatal(err)
	}

	noKey := testEnv(t)
	noKey.Vars["NOTESTEME_DATA"] = data
	delete(noKey.Vars, "NOTESTEME_MASTER_KEY")
	if _, err := OpenStandalone(t.Context(), noKey); !errors.Is(err, store.ErrLocked) {
		t.Fatalf("err = %v，預期 store.ErrLocked", err)
	}
	if fi, err := os.Stat(data); err != nil || fi.Mode().Perm() != 0o750 {
		t.Errorf("探測鎖時不應該改動資料目錄的權限：%v %v", fi, err)
	}
}

// 沒有人持有鎖、但資料目錄已存在時：沒有 master key 仍然失敗，而且鎖要釋放、權限不變。
func TestStandaloneWithoutMasterKeyOnExistingDir(t *testing.T) {
	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(data, 0o755); err != nil {
		t.Fatal(err)
	}
	delete(env.Vars, "NOTESTEME_MASTER_KEY")
	if _, err := OpenStandalone(t.Context(), env); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("err = %v，預期 ErrNoMasterKey", err)
	}
	if fi, err := os.Stat(data); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("master key 檢查失敗時不應該改動權限：%v %v", fi, err)
	}
	lock, err := store.LockDir(data)
	if err != nil {
		t.Fatalf("失敗後鎖應該已釋放：%v", err)
	}
	_ = lock.Close()
}

// serve 維持 SEC-R5 的順序：沒有 master key 時先失敗，既有目錄的權限也不動。
func TestServeWithoutMasterKeyLeavesExistingDirAlone(t *testing.T) {
	env := testEnv(t)
	data := env.Vars["NOTESTEME_DATA"]
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(data, 0o755); err != nil {
		t.Fatal(err)
	}
	delete(env.Vars, "NOTESTEME_MASTER_KEY")
	if _, err := StartServer(t.Context(), env); !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("err = %v，預期 ErrNoMasterKey", err)
	}
	if fi, err := os.Stat(data); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("serve 在 master key 檢查失敗時不應該改動權限：%v %v", fi, err)
	}
	if _, err := os.Stat(data + "/" + store.LockFileName); err == nil {
		t.Error("serve 在 master key 檢查失敗時不應該建立鎖檔")
	}
}
