package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewSeedsMedia(t *testing.T) {
	w := NewT(t)
	fi, err := os.Stat(filepath.Join(w.MediaDir(), "talks", "keynote.mp4"))
	if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 || fi.Size() > 1024 {
		t.Fatalf("media/talks/keynote.mp4 應是小檔案：%v %v", fi, err)
	}
	// 只讀 symlink 本身（Lstat、Readlink），不透過它存取任何東西。
	link := filepath.Join(w.MediaDir(), "link-to-root")
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link-to-root 應是 symlink：%v %v", fi, err)
	}
	if target, err := os.Readlink(link); err != nil || target != "/" {
		t.Fatalf("link-to-root -> %q, %v；應指向 /", target, err)
	}
}

func TestMediaVirtualPath(t *testing.T) {
	w := NewT(t)
	if got, want := w.Expand("/media/talks/keynote.mp4"), filepath.Join(w.Root, "media", "talks", "keynote.mp4"); got != want {
		t.Fatalf("Expand = %q, want %q", got, want)
	}
	if got := w.Unexpand(filepath.Join(w.Root, "media", "talks")); got != "/media/talks" {
		t.Fatalf("Unexpand = %q", got)
	}
}

// 場景根目錄含有指向 / 的 symlink；清除時不能跟隨它。
// 這個測試只用暫存目錄中的 symlink，指向另一個暫存目錄，不會對 / 做任何寫入或刪除。
func TestRemoveRootDoesNotFollowSymlinks(t *testing.T) {
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sub", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "media"), 0o700); err != nil {
		t.Fatal(err)
	}
	// 一個與 World 同名的 symlink（走 removeRoot 的明確移除分支），
	// 另一個在別的位置（只靠 RemoveAll 不跟隨 symlink）。
	for _, link := range []string{filepath.Join(root, "media", mediaLinkName), filepath.Join(root, "other-link")} {
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeRoot(root); err != nil {
		t.Fatalf("removeRoot: %v", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("根目錄應被清除：%v", err)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
		t.Fatalf("symlink 指向的目錄不應被清除：%v %q", err, b)
	}
}

func TestCloseRemovesRootWithoutTouchingLinkTarget(t *testing.T) {
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	root := w.Root
	// 把 link-to-root 換成指向暫存目錄的 symlink：Close 會走一樣的程式碼，卻不可能碰到 /。
	outside := t.TempDir()
	keep := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(w.MediaDir(), mediaLinkName)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatalf("根目錄應被清除：%v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("Close 跟隨了 symlink：%v", err)
	}
}
