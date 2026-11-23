package vault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openRoot(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, dir
}

func TestCheckInside(t *testing.T) {
	root, dir := openRoot(t)
	outside := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755))
	must(os.Symlink(outside, filepath.Join(dir, "out")))                          // 指向 vault 以外的資料夾
	must(os.Symlink("a", filepath.Join(dir, "in")))                               // 指向 vault 內（相對連結）
	must(os.Symlink(filepath.Join(dir, "a"), filepath.Join(dir, "abs")))          // 絕對路徑的連結：os.Root 一律視為逃逸
	must(os.Symlink("../../etc/passwd", filepath.Join(dir, "a", "b", "file.md"))) // 最後一段是逃逸的連結
	must(os.Symlink(filepath.Join(dir, "nope"), filepath.Join(dir, "dangling")))  // 懸空的連結
	must(os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644))

	ok := []string{"new/dir/file.md", "a/b/c.md", "a", "in/b/x.md"}
	for _, rel := range ok {
		if err := CheckInside(root, rel); err != nil {
			t.Errorf("%s：%v", rel, err)
		}
	}
	esc := map[string]string{"abs/b": "abs", "out/x.md": "out", "out": "out", "a/b/file.md": "a/b/file.md", "dangling/x": "dangling"}
	for rel, at := range esc {
		var e *EscapeError
		if err := CheckInside(root, rel); !errors.As(err, &e) || e.Path != at {
			t.Errorf("%s：err = %v，預期在 %s 的 EscapeError", rel, err, at)
		} else if e.Error() != "vault 內的 "+at+" 是指向 vault 以外的連結，已拒絕寫入。" {
			t.Errorf("訊息 = %q", e.Error())
		}
	}
	for _, rel := range []string{"", "/etc/passwd", "../x", "a/../../x", "a//b", "./a"} {
		var e *EscapeError
		if err := CheckInside(root, rel); err == nil || errors.As(err, &e) {
			t.Errorf("%q 應以路徑不正確拒絕：%v", rel, err)
		}
	}
	// os.Root 本身拒絕的錯誤也認得出來。
	_, err := root.Stat("out/secret")
	if !IsEscape(err) || IsEscape(nil) || IsEscape(errors.New("x")) {
		t.Errorf("IsEscape(%v) 判斷錯誤", err)
	}
}
