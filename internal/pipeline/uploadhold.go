package pipeline

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// uploadHolds 記錄進行中的備份數，以及備份期間被永久刪除、等備份結束才刪的上傳目錄（OPS-R1）。
//
// 上傳原始檔只存在資料目錄：備份在資料庫快照之後才讀這些檔案，這段期間被永久刪除的影音仍在快照中，
// 它的原始檔若已被刪掉，還原後就會 media_missing。程序在延後期間結束時，這些目錄沒有對應的影音，
// 下次啟動時由孤兒上傳目錄的清理刪除。
type uploadHolds struct {
	mu      sync.Mutex
	n       int
	pending []string
}

// HoldUploads 讓之後的永久刪除先不刪上傳原始檔，直到回傳的 release 被呼叫（可以重複呼叫，只有第一次有效）。
// 備份要在資料庫快照之前呼叫。可以同時有多個備份；最後一個 release 時才刪除延後的目錄。
func (p *Pipeline) HoldUploads() (release func()) {
	h := &p.uploadHolds
	h.mu.Lock()
	h.n++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.n--
			var dirs []string
			if h.n == 0 {
				dirs, h.pending = h.pending, nil
			}
			h.mu.Unlock()
			for _, d := range dirs {
				p.removeDir(context.Background(), d)
			}
		})
	}
}

// deferUpload 在有備份進行中時把 dir 留到備份結束才刪，回報是否延後。
func (h *uploadHolds) deferUpload(dir string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.n == 0 {
		return false
	}
	h.pending = append(h.pending, dir)
	return true
}

// isUploadDir 回報 dir 是不是資料目錄下 uploads/<id> 的影音目錄。
func (p *Pipeline) isUploadDir(dir string) bool {
	return filepath.Dir(dir) == filepath.Join(p.dataDir, "uploads")
}

// removeDir 刪除一個影音的資料目錄（包含目錄本身）；失敗只記 log。
// os.RemoveAll 不會跟隨目錄中的符號連結，連結指向的檔案不會被刪除。
func (p *Pipeline) removeDir(ctx context.Context, d string) {
	if err := os.RemoveAll(d); err != nil {
		p.log.LogAttrs(ctx, slog.LevelWarn, "永久刪除後清除檔案失敗", slog.String("dir", d), slog.String("error", err.Error()))
	}
}
