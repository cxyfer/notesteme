package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
)

// 現有的資料庫壞了、pre-restore 走複製時，-wal 與 -shm 也要一併保存：
// WAL 裡已提交的資料不能因為還原刪掉 -wal 而遺失。
func TestRestoreFallbackKeepsWALAndSHM(t *testing.T) {
	env := testEnv(t)
	file := makeBackup(t, env, "from-backup")
	data := env.Vars["NOTESTEME_DATA"]
	dbPath := filepath.Join(data, store.DBFileName)
	for path, content := range map[string]string{
		dbPath:          strings.Repeat("garbage", 1000),
		dbPath + "-wal": "wal-content",
		dbPath + "-shm": "shm-content",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{"-wal": "wal-content", "-shm": "shm-content"} {
		b, err := os.ReadFile(res.PreRestore + suffix)
		if err != nil {
			t.Fatalf("pre-restore 沒有保存 %s：%v", suffix, err)
		}
		if string(b) != want {
			t.Errorf("%s 的內容 = %q，預期 %q", suffix, b, want)
		}
	}
	// 原本的 -wal 與 -shm 仍要被刪除，不能套到新的資料庫上。
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); err == nil {
			t.Errorf("還原後 %s 不應該存在", suffix)
		}
	}
}

// 同一個時間點的 pre-restore 已存在時加上序號，不得覆蓋。
func TestRestorePreRestoreNeverOverwrites(t *testing.T) {
	env := testEnv(t)
	file := makeBackup(t, env, "from-backup")
	setMarker(t, env, "first")
	r1, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	setMarker(t, env, "second")
	r2, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	setMarker(t, env, "third")
	r3, err := Restore(t.Context(), env, file)
	if err != nil {
		t.Fatal(err)
	}
	if r1.PreRestore == r2.PreRestore || r2.PreRestore == r3.PreRestore || r1.PreRestore == r3.PreRestore {
		t.Fatalf("pre-restore 路徑重複：%s %s %s", r1.PreRestore, r2.PreRestore, r3.PreRestore)
	}
	if !strings.HasSuffix(r2.PreRestore, "-2.db") || !strings.HasSuffix(r3.PreRestore, "-3.db") {
		t.Errorf("序號不符：%s、%s", r2.PreRestore, r3.PreRestore)
	}
	for i, p := range []string{r1.PreRestore, r2.PreRestore, r3.PreRestore} {
		dir := t.TempDir()
		if err := os.Rename(p, filepath.Join(dir, store.DBFileName)); err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(t.Context(), dir, env.Clock)
		if err != nil {
			t.Fatal(err)
		}
		v, _, _ := st.GetSetting(t.Context(), "marker")
		_ = st.Close()
		if want := []string{"first", "second", "third"}[i]; v != want {
			t.Errorf("第 %d 份 pre-restore 的 marker = %q，預期 %q", i+1, v, want)
		}
	}
}
