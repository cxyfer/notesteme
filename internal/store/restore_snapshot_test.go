package store

import (
	"os"
	"path/filepath"
	"testing"
)

// 先刪 -wal、-shm 再換上快照：刪除失敗時，原本的資料庫不能已經被換掉，
// 否則會留下「新的主檔配舊的 -wal」這種不一致的狀態。
func TestRestoreSnapshotRemovesWALBeforeReplacing(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snap.db")
	path := filepath.Join(dir, "notesteme.db")
	if err := os.WriteFile(snapshot, []byte("snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	// -wal 是非空目錄：os.Remove 會失敗。
	wal := path + "-wal"
	if err := os.MkdirAll(filepath.Join(wal, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := restoreSnapshot(snapshot, path); err == nil {
		t.Fatal("刪不掉 -wal 時應該失敗")
	}
	if b, _ := os.ReadFile(path); string(b) != "current" {
		t.Errorf("刪除 -wal 失敗時不應該已經換掉主檔，內容 = %q", b)
	}
}

// 正常情況：主檔換成快照，-wal 與 -shm 都不在。
func TestRestoreSnapshotReplacesAndClearsWAL(t *testing.T) {
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snap.db")
	path := filepath.Join(dir, "notesteme.db")
	for p, c := range map[string]string{snapshot: "snapshot", path: "current", path + "-wal": "w", path + "-shm": "s"} {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := restoreSnapshot(snapshot, path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "snapshot" {
		t.Errorf("內容 = %q", b)
	}
	for _, s := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + s); err == nil {
			t.Errorf("%s 應該被刪除", s)
		}
	}
}
