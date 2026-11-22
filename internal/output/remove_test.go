package output

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(name string) bool {
		_, err := os.Lstat(filepath.Join(dir, name))
		return err == nil
	}

	write("same.srt", "hello")
	write("edited.srt", "hello by hand")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "target.srt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "target.srt"), filepath.Join(dir, "link.srt")); err != nil {
		t.Skip("無法建立 symlink：", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.srt"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		want     FileState
		stays    bool
		wantSHA  string
		fileName string
	}{
		{"沒被改過：刪除", StateIntact, false, sha("hello"), "same.srt"},
		{"改過：保留", StateModified, true, sha("hello"), "edited.srt"},
		{"不存在：略過", StateMissing, false, sha("hello"), "gone.srt"},
		{"symlink：不跟隨，保留", StateModified, true, sha("hello"), "link.srt"},
		{"目錄：保留", StateModified, true, sha("hello"), "sub.srt"},
	}
	for _, c := range cases {
		got, err := Remove(dir, c.fileName, c.wantSHA)
		if err != nil {
			t.Fatalf("%s：%v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s：狀態 %q，預期 %q", c.name, got, c.want)
		}
		if c.fileName != "gone.srt" && exists(c.fileName) != c.stays {
			t.Errorf("%s：檔案存在 = %v，預期 %v", c.name, exists(c.fileName), c.stays)
		}
	}
	// symlink 的目標沒有被動到。
	if b, err := os.ReadFile(filepath.Join(outside, "target.srt")); err != nil || string(b) != "hello" {
		t.Errorf("symlink 的目標被動到了：%q %v", b, err)
	}
}

func TestInspectDoesNotRemove(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.srt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Inspect(dir, "a.srt", sha("x"))
	if err != nil || st != StateIntact {
		t.Fatalf("Inspect = %q %v", st, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "a.srt")); err != nil {
		t.Fatal("Inspect 不能刪除檔案")
	}
	if st, _ := Inspect(filepath.Join(dir, "no-such-dir"), "a.srt", sha("x")); st != StateMissing {
		t.Errorf("資料夾不存在：%q，預期 missing", st)
	}
}

func TestRemoveRejectsBadName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../x", "a/b"} {
		if _, err := Remove(t.TempDir(), name, "x"); err == nil {
			t.Errorf("Remove(%q) 應該回傳錯誤", name)
		}
	}
}
