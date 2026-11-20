package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cxyfer/notesteme/internal/store"
)

// uuidName 是影音 ID（UUID）的字串格式；uploads 底下只有這種名稱的目錄才可能是孤兒。
var uuidName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// cleanOrphanUploads 刪除 <data>/uploads/ 底下沒有對應影音的 <id>/ 目錄。
//
// 上傳是先 rename 到 uploads/<id>/ 再開交易建立影音；中間 crash 會留下沒有影音的目錄。
// 必須在取得資料目錄的鎖之後、開始處理請求之前呼叫，否則會刪到正在上傳的檔案。
// 只處理直接子目錄、名稱是 UUID、且資料庫中（含垃圾桶）沒有這個 id 的影音；
// 不跟隨 symlink（symlink 不是目錄項目，不處理）。任何錯誤都只記 log，不阻止啟動。
func cleanOrphanUploads(ctx context.Context, st *store.Store, dataDir string, log *slog.Logger) {
	uploads := filepath.Join(dataDir, "uploads")
	if fi, err := os.Lstat(uploads); err != nil || !fi.IsDir() {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.WarnContext(ctx, "檢查 uploads 目錄失敗", "error", err)
		}
		return
	}
	ents, err := os.ReadDir(uploads)
	if err != nil {
		log.WarnContext(ctx, "讀取 uploads 目錄失敗", "error", err)
		return
	}
	for _, e := range ents {
		// DirEntry 的型別來自 lstat 語意：symlink 的 IsDir 是 false。
		if !e.IsDir() || !uuidName.MatchString(e.Name()) {
			continue
		}
		switch _, err := st.Read().ItemByID(ctx, e.Name()); {
		case err == nil:
			continue
		case !errors.Is(err, store.ErrNotFound):
			log.WarnContext(ctx, "查詢上傳目錄對應的影音失敗", "dir", e.Name(), "error", err)
			continue
		}
		if err := os.RemoveAll(filepath.Join(uploads, e.Name())); err != nil {
			log.WarnContext(ctx, "刪除孤兒上傳目錄失敗", "dir", e.Name(), "error", err)
			continue
		}
		log.InfoContext(ctx, "已刪除孤兒上傳目錄", "dir", e.Name())
	}
}
