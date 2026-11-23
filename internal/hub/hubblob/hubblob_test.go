package hubblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func TestFSPutOpenDelete(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "blobs")
	f, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	sha, n, err := f.Put(ctx, strings.NewReader("hello"), "")
	if err != nil || sha != sum("hello") || n != 5 {
		t.Fatalf("Put = %q %d %v", sha, n, err)
	}
	// 存在 <root>/<前 2 碼>/<sha256>，權限 0600。
	fi, err := os.Stat(filepath.Join(root, sha[:2], sha))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("檔案 = %v %v", fi, err)
	}
	// 相同內容再寫一次：同一個鍵，不重寫。
	if sha2, _, err := f.Put(ctx, strings.NewReader("hello"), sha); err != nil || sha2 != sha {
		t.Fatalf("第二次 Put = %q %v", sha2, err)
	}
	r, err := f.Open(ctx, sha)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "hello" {
		t.Errorf("內容 = %q", b)
	}
	if ok, _ := f.Exists(ctx, sha); !ok {
		t.Error("Exists 應該是 true")
	}
	if err := f.Delete(ctx, sha); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open(ctx, sha); !errors.Is(err, ErrNotFound) {
		t.Errorf("刪除後 Open = %v", err)
	}
	if err := f.Delete(ctx, sha); err != nil {
		t.Errorf("刪除不存在的 blob 不算錯誤：%v", err)
	}
	// 暫存檔不會留下。
	if left, _ := filepath.Glob(filepath.Join(root, ".put-*")); len(left) != 0 {
		t.Errorf("留下暫存檔 %v", left)
	}
}

// 內容與預期的 sha256 不符時什麼都不留下。
func TestFSHashMismatch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	f, _ := NewFS(root)
	_, _, err := f.Put(ctx, strings.NewReader("tampered"), sum("original"))
	if !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("err = %v", err)
	}
	if ok, _ := f.Exists(ctx, sum("tampered")); ok {
		t.Error("雜湊不符的內容不應該被保存")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Errorf("留下 %v", entries)
	}
}

// 鍵不是 sha256 時不組路徑（防止 ../ 讀到 blob 目錄以外的檔案）。
func TestFSRejectsInvalidKeys(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := NewFS(filepath.Join(dir, "blobs"))
	for _, k := range []string{"../secret", strings.Repeat("A", 64), "", strings.Repeat("a", 63)} {
		if _, err := f.Open(ctx, k); !errors.Is(err, ErrNotFound) {
			t.Errorf("Open(%q) = %v", k, err)
		}
		if ok, _ := f.Exists(ctx, k); ok {
			t.Errorf("Exists(%q) = true", k)
		}
	}
	if _, _, err := f.Put(ctx, strings.NewReader("x"), "../x"); err == nil {
		t.Error("Put 的 want 不是 sha256 時應該失敗")
	}
}

func TestS3NotImplemented(t *testing.T) {
	if s, err := NewS3(); s != nil || !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("NewS3 = %v %v", s, err)
	}
}
