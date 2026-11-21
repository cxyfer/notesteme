package pathguard

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	tmp := layout(t)
	p := func(parts ...string) string { return filepath.Join(append([]string{tmp}, parts...)...) }
	g := New([]string{p("media")})

	tests := []struct {
		name    string
		path    string
		want    string
		outside bool
	}{
		{"一般檔案", p("media", "talks", "keynote.mp4"), p("media", "talks", "keynote.mp4"), false},
		{"symlink 指向根內", p("media", "link-in", "keynote.mp4"), p("media", "talks", "keynote.mp4"), false},
		{"symlink 指向根外", p("media", "link-out", "secret.txt"), "", true},
		{".. 逃出", p("media", "..", "outside", "secret.txt"), "", true},
		{".. 留在根內", p("media", "talks", "..", "talks", "keynote.mp4"), p("media", "talks", "keynote.mp4"), false},
		{"不存在的檔案", p("media", "talks", "new.mp4"), p("media", "talks", "new.mp4"), false},
		{"相鄰名稱", p("media2", "secret.txt"), "", true},
		{"相對路徑", "media/talks/keynote.mp4", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := g.Resolve(tt.path)
			if tt.outside {
				if !errors.Is(err, ErrOutsideRoots) {
					t.Fatalf("err = %v，要 ErrOutsideRoots", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Resolve = %q, %v；要 %q", got, err, tt.want)
			}
		})
	}

	var nilGuard *Guard
	if _, err := nilGuard.Resolve(p("media")); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("nil Guard 要拒絕：%v", err)
	}

	// Unrestricted 只解析不限制。
	got, err := Unrestricted().Resolve(p("media", "link-out", "secret.txt"))
	if err != nil || got != p("outside", "secret.txt") {
		t.Fatalf("Unrestricted Resolve = %q, %v", got, err)
	}
	if _, err := Unrestricted().Resolve("rel/path"); err == nil {
		t.Fatal("相對路徑應該失敗")
	}
}

func TestOpen(t *testing.T) {
	tmp := layout(t)
	p := func(parts ...string) string { return filepath.Join(append([]string{tmp}, parts...)...) }
	g := New([]string{p("media")})

	t.Run("一般檔案", func(t *testing.T) {
		f, fi, err := g.Open(p("media", "talks", "keynote.mp4"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "x" || fi.Size() != 1 || !fi.Mode().IsRegular() {
			t.Fatalf("內容 %q，fi %v", b, fi)
		}
	})
	t.Run("根外", func(t *testing.T) {
		if _, _, err := g.Open(p("outside", "secret.txt")); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("不存在", func(t *testing.T) {
		if _, _, err := g.Open(p("media", "talks", "nope.mp4")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("目錄", func(t *testing.T) {
		if _, _, err := g.Open(p("media", "talks")); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("err = %v", err)
		}
		if _, _, err := g.Open(p("media")); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("根目錄本身：err = %v", err)
		}
	})
	t.Run("FIFO 不會卡住", func(t *testing.T) {
		fifo := p("media", "pipe")
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Skipf("無法建立 FIFO：%v", err)
		}
		done := make(chan error, 1)
		go func() {
			_, _, err := g.Open(fifo)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("err = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("開啟 FIFO 卡住")
		}
		// Unrestricted 也一樣。
		if _, _, err := Unrestricted().Open(fifo); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("Unrestricted：err = %v", err)
		}
	})
	t.Run("懸空 symlink", func(t *testing.T) {
		// 指向根內不存在的檔案：解析在根內，開啟時不存在。
		in := p("media", "dangling-in")
		if err := os.Symlink(p("media", "talks", "gone.mp4"), in); err != nil {
			t.Skip(err)
		}
		if _, _, err := g.Open(in); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("根內：err = %v", err)
		}
		// 指向根外不存在的檔案：拒絕。
		out := p("media", "dangling-out")
		if err := os.Symlink(p("outside", "gone.txt"), out); err != nil {
			t.Skip(err)
		}
		if _, _, err := g.Open(out); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("根外：err = %v", err)
		}
	})
	t.Run("檔案被換成指向根外的 symlink", func(t *testing.T) {
		target := p("media", "swap.mp4")
		if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
			t.Fatal(err)
		}
		f, _, err := g.Open(target)
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p("outside", "secret.txt"), target); err != nil {
			t.Skip(err)
		}
		if _, _, err := g.Open(target); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("解析之後才被換掉（openUnder）", func(t *testing.T) {
		// 模擬 Resolve 之後路徑被換成 symlink：直接對 openUnder 傳入含 symlink 的相對路徑。
		root := p("media")
		if _, err := openUnder(root, filepath.Join(root, "link-out", "secret.txt")); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("目錄 symlink：err = %v", err)
		}
	})
	t.Run("Unrestricted", func(t *testing.T) {
		f, _, err := Unrestricted().Open(p("outside", "secret.txt"))
		if err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		if _, _, err := Unrestricted().Open(p("outside")); !errors.Is(err, ErrNotRegular) {
			t.Fatalf("目錄：err = %v", err)
		}
	})
	t.Run("nil Guard", func(t *testing.T) {
		var n *Guard
		if _, _, err := n.Open(p("media", "talks", "keynote.mp4")); !errors.Is(err, ErrOutsideRoots) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestWith(t *testing.T) {
	tmp := layout(t)
	p := func(parts ...string) string { return filepath.Join(append([]string{tmp}, parts...)...) }
	base := New([]string{p("media")})
	g := base.With(p("outside"), "relative/ignored", "")

	if err := g.Check(p("outside", "secret.txt")); err != nil {
		t.Fatalf("新增的 root 應生效：%v", err)
	}
	if f, _, err := g.Open(p("outside", "secret.txt")); err != nil {
		t.Fatalf("Open：%v", err)
	} else {
		_ = f.Close()
	}
	if err := g.Check(p("media", "talks", "keynote.mp4")); err != nil {
		t.Fatalf("原本的 root 仍有效：%v", err)
	}
	// 原 Guard 不受影響。
	if err := base.Check(p("outside", "secret.txt")); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("原 Guard 不應改變：%v", err)
	}
	if err := g.Check(p("media2", "secret.txt")); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("其他路徑仍拒絕：%v", err)
	}
	if !Unrestricted().With(p("media")).unrestricted {
		t.Fatal("Unrestricted 的 With 仍不受限制")
	}
	var n *Guard
	if err := n.With(p("outside")).Check(p("outside", "secret.txt")); err != nil {
		t.Fatalf("nil Guard 的 With：%v", err)
	}
}
