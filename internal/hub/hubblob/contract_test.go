package hubblob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

// runContract 是每一種 Store 都要通過的共用案例（FS、S3 的假服務、真的 MinIO）。
func runContract(t *testing.T, newStore func(t *testing.T) Store) {
	ctx := context.Background()
	read := func(t *testing.T, s Store, sha string) string {
		t.Helper()
		r, err := s.Open(ctx, sha)
		if err != nil {
			t.Fatalf("Open(%s)：%v", sha, err)
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("讀取 %s：%v", sha, err)
		}
		return string(b)
	}

	t.Run("PutOpenExistsDelete", func(t *testing.T) {
		s := newStore(t)
		sha, n, err := s.Put(ctx, strings.NewReader("hello"), "")
		if err != nil || sha != sum("hello") || n != 5 {
			t.Fatalf("Put = %q %d %v", sha, n, err)
		}
		// 相同內容再寫一次（帶預期的雜湊）：同一個鍵，不算錯誤。
		if sha2, n2, err := s.Put(ctx, strings.NewReader("hello"), sha); err != nil || sha2 != sha || n2 != 5 {
			t.Fatalf("第二次 Put = %q %d %v", sha2, n2, err)
		}
		if got := read(t, s, sha); got != "hello" {
			t.Errorf("內容 = %q", got)
		}
		if ok, err := s.Exists(ctx, sha); !ok || err != nil {
			t.Errorf("Exists = %v %v", ok, err)
		}
		if err := s.Delete(ctx, sha); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Open(ctx, sha); !errors.Is(err, ErrNotFound) {
			t.Errorf("刪除後 Open = %v", err)
		}
		if ok, err := s.Exists(ctx, sha); ok || err != nil {
			t.Errorf("刪除後 Exists = %v %v", ok, err)
		}
		if err := s.Delete(ctx, sha); err != nil {
			t.Errorf("刪除不存在的 blob 不算錯誤：%v", err)
		}
	})

	t.Run("EmptyAndLargeAndNonSeekable", func(t *testing.T) {
		s := newStore(t)
		if sha, n, err := s.Put(ctx, bytes.NewReader(nil), ""); err != nil || sha != sum("") || n != 0 {
			t.Fatalf("空的內容 = %q %d %v", sha, n, err)
		} else if got := read(t, s, sha); got != "" {
			t.Errorf("空的內容讀回 %q", got)
		}
		big := strings.Repeat("0123456789abcdef", 1<<16) // 1 MiB
		// io.MultiReader 不是 io.ReadSeeker：要先完整讀進來才能算雜湊。
		sha, n, err := s.Put(ctx, io.MultiReader(strings.NewReader(big[:7]), strings.NewReader(big[7:])), sum(big))
		if err != nil || sha != sum(big) || n != int64(len(big)) {
			t.Fatalf("1 MiB = %q %d %v", sha, n, err)
		}
		if got := read(t, s, sha); got != big {
			t.Errorf("1 MiB 讀回的內容不同（%d bytes）", len(got))
		}
	})

	t.Run("HashMismatchStoresNothing", func(t *testing.T) {
		s := newStore(t)
		if _, _, err := s.Put(ctx, strings.NewReader("tampered"), sum("original")); !errors.Is(err, ErrHashMismatch) {
			t.Fatalf("err = %v", err)
		}
		for _, k := range []string{sum("tampered"), sum("original")} {
			if ok, _ := s.Exists(ctx, k); ok {
				t.Errorf("雜湊不符時不應該保存 %s", k)
			}
		}
	})

	t.Run("InvalidKeys", func(t *testing.T) {
		s := newStore(t)
		for _, k := range []string{"../secret", strings.Repeat("A", 64), "", strings.Repeat("a", 63), strings.Repeat("a", 64) + "/../x", "a b"} {
			if _, err := s.Open(ctx, k); !errors.Is(err, ErrNotFound) {
				t.Errorf("Open(%q) = %v", k, err)
			}
			if ok, err := s.Exists(ctx, k); ok || err != nil {
				t.Errorf("Exists(%q) = %v %v", k, ok, err)
			}
			if err := s.Delete(ctx, k); err != nil {
				t.Errorf("Delete(%q) = %v", k, err)
			}
		}
		if _, _, err := s.Put(ctx, strings.NewReader("x"), "../x"); err == nil {
			t.Error("Put 的 want 不是 sha256 時應該失敗")
		}
	})

	t.Run("ConcurrentDuplicateUploads", func(t *testing.T) {
		s := newStore(t)
		content := strings.Repeat("dup", 1000)
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if sha, _, err := s.Put(ctx, strings.NewReader(content), sum(content)); err != nil || sha != sum(content) {
					errs <- errors.Join(err, errors.New(sha))
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if got := read(t, s, sum(content)); got != content {
			t.Error("並行上傳相同內容後讀回的內容不同")
		}
	})
}
