package backup

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSaveWithWritesAndUpdatesLatest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new")
	name := FileName(t0)
	path, err := SaveWith(dir, name, func(w io.Writer) error {
		_, err := io.WriteString(w, "archive")
		return err
	})
	if err != nil || path != filepath.Join(dir, name) {
		t.Fatalf("SaveWith = %q，%v", path, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("備份檔的權限 = %v，%v", fi, err)
	}
	if target, err := os.Readlink(filepath.Join(dir, LatestName)); err != nil || target != name {
		t.Fatalf("latest = %q，%v（必須是相對 symlink）", target, err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("新建立的目錄權限 = %v", fi.Mode().Perm())
	}
}

func TestSaveWithFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := SaveWith(dir, "../escape.tar.gz", func(io.Writer) error { return nil }); err == nil {
		t.Fatal("不合格式的檔名要拒絕")
	}
	boom := errors.New("disk full")
	if _, err := SaveWith(dir, FileName(t0), func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("寫入失敗要原樣回傳：%v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("失敗後不能留下暫存檔或 latest：%v", entries)
	}
	// 目的地是檔案而不是目錄。
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveWith(file, FileName(t0), func(io.Writer) error { return nil }); err == nil {
		t.Error("目的地不是目錄時要失敗")
	}
}

// 手動備份與每日備份同時寫入同一個目錄：各自的檔案都要完整，latest 指向其中一份，不留下暫存的 symlink。
func TestConcurrentSavesInSameDirectory(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := FileName(t0.Add(time.Duration(i) * time.Second))
			_, err := SaveWith(dir, name, func(w io.Writer) error {
				_, err := io.WriteString(w, strings.Repeat("x", 1024))
				return err
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 17 {
		t.Fatalf("目錄中有 %d 個項目，預期 16 份備份加 latest：%v", len(entries), entries)
	}
	target, err := os.Readlink(filepath.Join(dir, LatestName))
	if err != nil || !ValidFileName(target) {
		t.Fatalf("latest = %q，%v", target, err)
	}
}

func TestFreeSpaceReportsSomething(t *testing.T) {
	n, err := FreeSpace(t.TempDir())
	if err != nil && !errors.Is(err, ErrSpaceUnknown) {
		t.Fatal(err)
	}
	if err == nil && n == 0 {
		t.Error("暫存目錄的剩餘空間不該是 0")
	}
}
