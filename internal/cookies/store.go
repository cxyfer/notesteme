package cookies

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cxyfer/notesteme/internal/secrets"
	"github.com/cxyfer/notesteme/internal/store"
)

// secretKind 是 cookie 在 secrets 表中的 kind；name 是平台。AAD 是 "cookie/<platform>"。
const secretKind = "cookie"

// Store 加密存放各平台的 cookie，並在執行時提供暫存檔。每個平台只存一份。
type Store struct {
	st      *store.Store
	keys    *secrets.Keyring
	tmpDir  string
	randHex func() (string, error)
}

// New 建立 Store。暫存檔寫在 dataDir/tmp。
func New(st *store.Store, keys *secrets.Keyring, dataDir string) *Store {
	return &Store{st: st, keys: keys, tmpDir: filepath.Join(dataDir, "tmp"), randHex: randomHex}
}

// Put 正規化 content、加密後存起來，取代該平台原本的 cookie。回傳 cookie 數量與更新時間。
// 正規化的錯誤見 Err*；失敗時不改動已存的 cookie。
func (s *Store) Put(ctx context.Context, platform, content string) (count int, updatedAt time.Time, err error) {
	normalized, count, err := Normalize(platform, content)
	if err != nil {
		return 0, time.Time{}, err
	}
	nonce, ct, err := s.keys.Seal(secretKind, platform, []byte(normalized))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("加密 cookie 失敗：%w", err)
	}
	w := s.st.Write()
	if err := w.UpsertSecret(ctx, secretKind, platform, nonce, ct); err != nil {
		return 0, time.Time{}, fmt.Errorf("儲存 cookie 失敗：%w", err)
	}
	sec, err := w.Secret(ctx, secretKind, platform)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("讀取 cookie 的更新時間失敗：%w", err)
	}
	return count, sec.UpdatedAt, nil
}

// Acquire 取出平台的 cookie，寫成 dataDir/tmp 下權限 0600 的暫存檔並回傳路徑（SEC-R7）。
// 每次呼叫一個新檔名：yt-dlp 結束時會截斷並重寫 cookie 檔，共用檔案在並行時會讀到寫到一半的內容。
// 呼叫端用完要呼叫 release 刪除檔案；release 可重複呼叫。
//
// 沒有 cookie 時回傳空路徑與 no-op 的 release。失敗時 release 也是 no-op，錯誤不含 cookie 內容。
func (s *Store) Acquire(ctx context.Context, platform string) (path string, release func(), err error) {
	noop := func() {}
	if !ValidPlatform(platform) {
		return "", noop, ErrUnknownPlatform
	}
	sec, err := s.st.Read().Secret(ctx, secretKind, platform)
	if errors.Is(err, store.ErrNotFound) {
		return "", noop, nil
	}
	if err != nil {
		return "", noop, fmt.Errorf("讀取 cookie 失敗：%w", err)
	}
	plain, err := s.keys.Open(secretKind, platform, sec.Nonce, sec.Ciphertext)
	if err != nil {
		return "", noop, fmt.Errorf("解開 %s 的 cookie 失敗：%w", platform, err)
	}
	if err := os.MkdirAll(s.tmpDir, 0o700); err != nil {
		return "", noop, fmt.Errorf("建立暫存目錄失敗：%w", err)
	}
	// O_EXCL：檔名撞到時不覆寫既有的檔案；隨機名稱撞到的機率可忽略，撞到就重試幾次。
	for range 5 {
		suffix, err := s.randHex()
		if err != nil {
			return "", noop, err
		}
		p := filepath.Join(s.tmpDir, "cookies-"+platform+"-"+suffix+".txt")
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", noop, fmt.Errorf("建立 cookie 暫存檔失敗：%w", err)
		}
		_, werr := f.Write(plain)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = os.Remove(p)
			return "", noop, errors.New("寫入 cookie 暫存檔失敗")
		}
		return p, func() { _ = os.Remove(p) }, nil
	}
	return "", noop, errors.New("無法建立不重複的 cookie 暫存檔")
}

func randomHex() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
