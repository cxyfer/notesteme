package pathguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// dangling symlink（指向不存在的目標）不能被當成「不存在的元件」直接放行，要檢查它指向哪裡。
func TestCheckDanglingSymlink(t *testing.T) {
	tmp := layout(t)
	p := func(parts ...string) string { return filepath.Join(append([]string{tmp}, parts...)...) }
	links := map[string]string{
		"media/dangling-out":     filepath.Join(tmp, "outside", "missing"),
		"media/danglingdir-out":  filepath.Join(tmp, "outside", "nodir"),
		"media/dangling-in":      filepath.Join(tmp, "media", "talks", "later.mp4"),
		"media/dangling-rel-out": "../outside/missing",
		"media/dangling-rel-in":  "talks/later.mp4",
		"media/chain":            filepath.Join(tmp, "media", "dangling-out"), // 多跳：先指到根內的 symlink，再指到根外
		"media/loop-a":           filepath.Join(tmp, "media", "loop-b"),
		"media/loop-b":           filepath.Join(tmp, "media", "loop-a"),
	}
	for link, target := range links {
		if err := os.Symlink(target, filepath.Join(tmp, link)); err != nil {
			t.Skipf("無法建立 symlink：%v", err)
		}
	}
	tests := []struct {
		name string
		path string
		ok   bool
	}{
		{"dangling 指向根外", p("media/dangling-out"), false},
		{"dangling 在中間元件，指向根外", p("media/danglingdir-out/sub/x.mp3"), false},
		{"dangling 相對目標指向根外", p("media/dangling-rel-out"), false},
		{"dangling 多跳後指向根外", p("media/chain"), false},
		{"dangling 指向根內不存在的檔案", p("media/dangling-in"), true},
		{"dangling 相對目標指向根內", p("media/dangling-rel-in"), true},
		{"symlink 迴圈", p("media/loop-a"), false},
		{"symlink 迴圈在中間元件", p("media/loop-a/x.mp3"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := New([]string{p("media")}).Check(tc.path)
			if tc.ok && err != nil {
				t.Fatalf("Check(%q) = %v，應該通過", tc.path, err)
			}
			if !tc.ok && !errors.Is(err, ErrOutsideRoots) {
				t.Fatalf("Check(%q) = %v，應該是 ErrOutsideRoots", tc.path, err)
			}
		})
	}
}

// 很長的 dangling symlink 鏈超過深度上限時一律拒絕（即使最後指向根內）。
func TestCheckDanglingChainTooDeep(t *testing.T) {
	tmp := layout(t)
	media := filepath.Join(tmp, "media")
	next := filepath.Join(media, "missing-final")
	for i := 0; i < 50; i++ {
		link := filepath.Join(media, fmt.Sprintf("hop%02d", i))
		if err := os.Symlink(next, link); err != nil {
			t.Skipf("無法建立 symlink：%v", err)
		}
		next = link
	}
	if err := New([]string{media}).Check(next); !errors.Is(err, ErrOutsideRoots) {
		t.Fatalf("超過深度上限應拒絕，得到 %v", err)
	}
}
