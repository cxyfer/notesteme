package cookies

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

type rig struct {
	dir string
	st  *store.Store
	cs  *Store
	clk *clock.Fake
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC))
	st, err := store.Open(ctx, dir, clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	keys, err := secrets.Open(ctx, st, "test-master-key", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	return &rig{dir: dir, st: st, cs: New(st, keys, dir), clk: clk}
}

const ytCookie = "SID=very-secret-sid; HSID=very-secret-hsid"

func TestPutEncryptsAndCounts(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	n, at, err := r.cs.Put(ctx, "youtube", ytCookie)
	if err != nil || n != 2 || !at.Equal(r.clk.Now()) {
		t.Fatalf("Put = %d, %v, %v", n, at, err)
	}

	sec, err := r.st.Read().Secret(ctx, "cookie", "youtube")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sec.Ciphertext, []byte("very-secret")) {
		t.Error("secrets 表的密文含明文")
	}
	// 資料目錄中的所有檔案（DB、WAL）都不得出現明文。
	err = filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(b, []byte("very-secret")) {
			t.Errorf("%s 含 cookie 明文", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPutReplacesPrevious(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, _, err := r.cs.Put(ctx, "youtube", "A=1; B=2; C=3"); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Hour)
	n, at, err := r.cs.Put(ctx, "youtube", "D=4")
	if err != nil || n != 1 || !at.Equal(r.clk.Now()) {
		t.Fatalf("Put = %d, %v, %v", n, at, err)
	}
	path, release, err := r.cs.Acquire(ctx, "youtube")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "\tA\t") || !strings.Contains(string(b), "\tD\t4\n") {
		t.Errorf("新的 cookie 應取代舊的：%q", b)
	}
}

func TestPutRejectsBadInputWithoutStoring(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	for _, c := range []struct{ platform, content string }{
		{"youtube", ""}, {"twitter", "a=1"}, {"youtube/x", "a=1"}, {"bilibili", "a=1"},
	} {
		if _, _, err := r.cs.Put(ctx, c.platform, c.content); err == nil {
			t.Errorf("Put(%q, %q) 應該失敗", c.platform, c.content)
		}
	}
	if list, _ := r.st.Read().ListSecrets(ctx); len(list) != 0 {
		t.Errorf("失敗時不應存入任何 secret：%+v", list)
	}
}

func TestAcquireWithoutCookie(t *testing.T) {
	r := newRig(t)
	path, release, err := r.cs.Acquire(context.Background(), "youtube")
	if err != nil || path != "" || release == nil {
		t.Fatalf("Acquire = %q, %v, %v", path, release != nil, err)
	}
	release() // no-op，不得 panic
	if _, err := os.Stat(filepath.Join(r.dir, "tmp")); err == nil {
		if entries, _ := os.ReadDir(filepath.Join(r.dir, "tmp")); len(entries) != 0 {
			t.Errorf("沒有 cookie 時不應建立任何檔案：%v", entries)
		}
	}
}

func TestAcquireWritesPrivateTempFileAndReleaseDeletesIt(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, _, err := r.cs.Put(ctx, "youtube", ytCookie); err != nil {
		t.Fatal(err)
	}
	path, release, err := r.cs.Acquire(ctx, "youtube")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(r.dir, "tmp") {
		t.Errorf("檔案應在 <data>/tmp：%s", path)
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "cookies-youtube-") || !strings.HasSuffix(base, ".txt") {
		t.Errorf("檔名 = %s", base)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("權限 = %v，預期 0600", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(path)
	want := netscapeHeader +
		".youtube.com\tTRUE\t/\tTRUE\t0\tSID\tvery-secret-sid\n" +
		".youtube.com\tTRUE\t/\tTRUE\t0\tHSID\tvery-secret-hsid\n"
	if string(b) != want {
		t.Errorf("內容 = %q", b)
	}

	release()
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("release 之後檔案應已刪除：%v", err)
	}
	release() // 重複 release 不得 panic
}

func TestAcquireTwiceGivesDifferentFiles(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, _, err := r.cs.Put(ctx, "youtube", ytCookie); err != nil {
		t.Fatal(err)
	}
	p1, rel1, err := r.cs.Acquire(ctx, "youtube")
	if err != nil {
		t.Fatal(err)
	}
	p2, rel2, err := r.cs.Acquire(ctx, "youtube")
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("同時兩次呼叫得到同一個檔名：%s", p1)
	}
	rel1()
	if _, err := os.Stat(p2); err != nil {
		t.Errorf("釋放第一個不應影響第二個：%v", err)
	}
	rel2()
}

func TestAcquireRejectsUnknownPlatform(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{"twitter", "", "youtube/../../x"} {
		if _, _, err := r.cs.Acquire(context.Background(), p); !errors.Is(err, ErrUnknownPlatform) {
			t.Errorf("Acquire(%q) err = %v", p, err)
		}
	}
}

func TestAcquireErrorDoesNotLeakPlaintext(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if _, _, err := r.cs.Put(ctx, "youtube", ytCookie); err != nil {
		t.Fatal(err)
	}
	// 換一把資料金鑰：解密必須失敗，且錯誤不帶任何內容。
	other, err := secrets.Open(ctx, openOther(t), "other-key", secrets.TestKDF)
	if err != nil {
		t.Fatal(err)
	}
	bad := New(r.st, other, r.dir)
	path, release, err := bad.Acquire(ctx, "youtube")
	if err == nil {
		release()
		t.Fatalf("用錯的金鑰應該解密失敗，path = %s", path)
	}
	if path != "" || release == nil {
		t.Errorf("失敗時 path 應為空且 release 為 no-op")
	}
	if strings.Contains(err.Error(), "very-secret") {
		t.Errorf("錯誤含明文：%v", err)
	}
}

func openOther(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir(), clock.NewFake(time.Unix(1_700_000_000, 0)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
