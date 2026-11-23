// Package hubblob 是 Hub 的檔案儲存（ADR-0030 第 2 點）：以內容的 sha256 為鍵，天然去重。
//
// 實作：FS（參考實作，存在 <data>/blobs/<sha256 前 2 碼>/<sha256>）與 S3（R2、MinIO、AWS S3，見 s3.go）。
// blob 的內容是使用者上傳的不可信資料：Put 自己計算 sha256，呼叫端給的雜湊只用來比對。
package hubblob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// ErrNotFound 表示沒有這個 blob。
var ErrNotFound = errors.New("blob 不存在")

// ErrHashMismatch 表示內容的 sha256 和預期的不同。
var ErrHashMismatch = errors.New("blob 的內容與 sha256 不符")

// ErrNotImplemented 表示這個 driver 還沒有實作（目前沒有使用；保留給之後的 driver）。
var ErrNotImplemented = errors.New("尚未實作")

// Store 是 blob 的儲存介面。
type Store interface {
	// Put 寫入 r 的內容並回傳 sha256（小寫十六進位）與大小；want 不是空字串時內容必須相符，否則什麼都不留下。
	// 已經存在時不重寫。
	Put(ctx context.Context, r io.Reader, want string) (sha string, size int64, err error)
	// Open 讀取 blob；不存在時回 ErrNotFound。
	Open(ctx context.Context, sha string) (io.ReadCloser, error)
	// Exists 回報 blob 是否存在。
	Exists(ctx context.Context, sha string) (bool, error)
	// Delete 刪除 blob；不存在時不算錯誤（下架時刪除沒有其他版本引用的 blob，HACC-R16）。
	Delete(ctx context.Context, sha string) error
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA 回報 s 是否是小寫十六進位的 sha256；路徑由它組出，所以一律先檢查（防止 ../ 之類的鍵）。
func ValidSHA(s string) bool { return shaPattern.MatchString(s) }

// FS 是檔案系統上的 blob 儲存。
type FS struct{ root string }

var _ Store = (*FS)(nil)

// NewFS 以 root（通常是 <data>/blobs）建立 FS；目錄不存在時以 0700 建立。
func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("建立 blob 目錄失敗：%w", err)
	}
	return &FS{root: root}, nil
}

func (f *FS) path(sha string) string { return filepath.Join(f.root, sha[:2], sha) }

// Put 實作 Store：先寫到同一個目錄下的暫存檔、驗證雜湊，再 rename（同一個檔案系統上是原子的）。
func (f *FS) Put(ctx context.Context, r io.Reader, want string) (string, int64, error) {
	if want != "" && !ValidSHA(want) {
		return "", 0, fmt.Errorf("不是有效的 sha256：%q", want)
	}
	tmp, err := os.CreateTemp(f.root, ".put-*")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(tmp.Name()) // rename 之後這個名稱已不存在，Remove 失敗無妨
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, err
	}
	sha := hex.EncodeToString(h.Sum(nil))
	if want != "" && sha != want {
		return "", 0, ErrHashMismatch
	}
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	dst := f.path(sha)
	if _, err := os.Stat(dst); err == nil {
		return sha, n, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", 0, err
	}
	return sha, n, nil
}

// Open 實作 Store。
func (f *FS) Open(_ context.Context, sha string) (io.ReadCloser, error) {
	if !ValidSHA(sha) {
		return nil, ErrNotFound
	}
	r, err := os.Open(f.path(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return r, err
}

// Exists 實作 Store。
func (f *FS) Exists(_ context.Context, sha string) (bool, error) {
	if !ValidSHA(sha) {
		return false, nil
	}
	_, err := os.Stat(f.path(sha))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Delete 實作 Store。
func (f *FS) Delete(_ context.Context, sha string) error {
	if !ValidSHA(sha) {
		return nil
	}
	if err := os.Remove(f.path(sha)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
