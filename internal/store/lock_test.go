package store

import "testing"

// 取得鎖之後立刻清空鎖檔：上一個持有者（例如被 Kill 的 serve）留下的內容不能被讀成現在的持有者，
// 否則另一個程序會在新持有者寫入之前，連到已經不存在的 socket。
func TestLockDirTruncatesStaleInfo(t *testing.T) {
	dir := t.TempDir()
	l, err := LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.WriteInfo(LockInfo{Holder: "serve", PID: 1, Listen: "127.0.0.1:1", Socket: "/x.sock"}); err != nil {
		t.Fatal(err)
	}
	_ = l.Close() // 模擬程序中斷：內容留著

	if info, err := ReadLockInfo(dir); err != nil || info == nil {
		t.Fatalf("Close 不應該清空內容：%+v %v", info, err)
	}
	l2, err := LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if info, err := ReadLockInfo(dir); err != nil || info != nil {
		t.Errorf("取得鎖之後應該立刻清空舊內容：%+v %v", info, err)
	}
}
