package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/clock"
)

// 測試 binary 中，新的資料庫改從「已套用全部內建 migration 的範本」複製，不再每次從頭跑 migration。
//
// modernc.org/sqlite 是純 Go 的 SQLite，-race 會 instrument 整個 SQLite，從零套用全部 migration
// 在 -race 下每次要 1～2 秒（不開 -race 約 0.1 秒）；上千個測試各開一次，CI 的 go test 因此要 30 分鐘以上。
// 範本由同一個 process 第一次開啟新資料庫時，以正常的 Open 流程（migrate、建立搜尋索引）產生一次，
// 所以 fresh migration 的路徑每個測試 binary 仍會實際執行；store 自己的 migration 測試用 Options.Migrations
// 或直接操作 goose，不經過範本。
//
// 只在 testing.Testing() 為 true（go test 產生的 binary）且使用內建 migration 時生效；正式 binary 一律走原本的流程。
// 設定 NOTESTEME_STORE_NO_TEMPLATE=1 可在測試中關閉，每次都從頭套用 migration。
var freshTemplate struct {
	once sync.Once
	data []byte
	err  error
}

// useFreshTemplate 回報這次 Open 是否可以用範本建立新的資料庫。
func useFreshTemplate(o Options) bool {
	return o.Migrations == nil && testing.Testing() && os.Getenv("NOTESTEME_STORE_NO_TEMPLATE") == ""
}

// seedFromTemplate 在 path 還不存在時，把範本複製成 path（0600）。path 已存在（或無法判斷）時不做任何事，
// 交給原本的流程處理。
func seedFromTemplate(dir, path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	// 只有資料庫本身不存在、但留有 -wal／-shm 時，交給原本的流程（不覆蓋任何既有檔案）。
	for _, p := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			return nil
		}
	}
	freshTemplate.once.Do(func() { freshTemplate.data, freshTemplate.err = buildFreshTemplate() })
	if freshTemplate.err != nil {
		return fmt.Errorf("建立測試用的資料庫範本失敗：%w", freshTemplate.err)
	}
	if _, err := os.Stat(dir); err != nil {
		// dir 必須已存在（與 Open 的約定相同）；讓原本的流程回報錯誤。
		return nil
	}
	return WriteFileAtomic(path, bytes.NewReader(freshTemplate.data))
}

// buildFreshTemplate 以正常的 Open 流程建立一個新的資料庫，關閉（WAL checkpoint）後讀回檔案內容。
func buildFreshTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "notesteme-store-template-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	st, err := open(context.Background(), dir, clock.Real{}, Options{}, false)
	if err != nil {
		return nil, err
	}
	// 先把 WAL 的內容寫回主檔並截斷，複製單一檔案才會是完整的資料庫。
	if _, err := st.w.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, DBFileName)
	if fi, err := os.Stat(path + "-wal"); err == nil && fi.Size() > 0 {
		return nil, fmt.Errorf("關閉後仍有未寫回的 WAL（%d bytes）", fi.Size())
	}
	return os.ReadFile(path)
}
