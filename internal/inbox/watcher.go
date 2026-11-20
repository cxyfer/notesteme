// Package inbox 監看 inbox 目錄，把穩定的影音檔自動新增到媒體庫（ING-05、ING-R10、ING-R11）。
//
// 每個檔案依序經過：穩定性判斷（大小與修改時間連續 10 秒不變）→ ffprobe 篩選 → sha256 → IngestLocal → 記錄判定。
// 判定存在 inbox_entries：同一個 (size, mtime) 不會重複處理；檔案消失時刪除紀錄。
// 時間一律來自注入的 clock，不呼叫 time.Now。
package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/pathguard"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/store"
)

const (
	// PollInterval 是 Loop 兩次掃描之間的間隔。
	PollInterval = 5 * time.Second
	// StableFor 是檔案大小與修改時間連續不變多久才算穩定（ING-R10）。
	StableFor = 10 * time.Second
	// maxDepth 是路徑最多幾層（含檔名）；maxFiles 是每輪最多處理幾個檔案。
	maxDepth = 8
	maxFiles = 10_000
)

// Ingester 新增本機檔案（*pipeline.Pipeline 實作它）。
type Ingester interface {
	IngestLocal(ctx context.Context, t pipeline.LocalTarget) (pipeline.LocalOutcome, error)
}

// Watcher 掃描 inbox。Run 與 NextAt 實作 worker.Periodic（手動模式每一輪都掃描）；
// production 用 Loop 每 PollInterval 掃描一次。
type Watcher struct {
	Clock clock.Clock
	// Inbox 是 inbox 目錄。
	Inbox string
	// Files 是程式內讀取檔案的守衛（雜湊用 Open、外部工具用 Resolve）。
	Files    *pathguard.Guard
	Media    *media.Tool
	Store    *store.Store
	Ingester Ingester
	Log      *slog.Logger

	mu sync.Mutex
	// seen 是還沒有判定之前觀察到的檔案狀態（只在記憶體，重啟後重新計時）。
	seen map[string]seenFile
	// warned 記錄已經記過 log 的暫時性失敗（以檔案的 size 與 mtime 為準），避免每 5 秒重複。
	warned   map[string]string
	lastScan time.Time
	scanned  bool
}

type seenFile struct {
	size  int64
	mtime time.Time
	since time.Time
}

// file 是掃描到的一個檔案。
type file struct {
	rel   string
	size  int64
	mtime time.Time
}

func (w *Watcher) log() *slog.Logger {
	if w.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return w.Log
}

// Run 掃描一次 inbox（worker.Periodic）。暫時性的失敗只記 log，下一輪重試；只有 ctx 取消與資料庫錯誤會回傳。
func (w *Watcher) Run(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.scan(ctx)
	w.lastScan, w.scanned = w.Clock.Now(), true
	return err
}

// NextAt 回傳下一次掃描的時間（worker.Periodic）：上一次掃描之後 PollInterval；還沒掃描過時 ok 為 false。
func (w *Watcher) NextAt(context.Context) (time.Time, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.scanned {
		return time.Time{}, false, nil
	}
	return w.lastScan.Add(PollInterval), true, nil
}

// Loop 是 production 的主迴圈：每 PollInterval 掃描一次，直到 ctx 取消。
func (w *Watcher) Loop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := w.Run(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("inbox 掃描失敗", "error", err)
		}
		t := w.Clock.NewTimer(PollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
	}
}

// skipName 回報檔名是否不該當成影音：隱藏檔、下載中與暫存檔、字幕附屬檔。
func skipName(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".part", ".crdownload", ".tmp", ".download", ".srt", ".vtt", ".ass", ".ssa", ".sub", ".idx":
		return true
	}
	return false
}

// list 列出 inbox 中候選的檔案，依 (mtime, 名稱) 排序。truncated 為 true 代表超過 maxFiles，清單不完整。
// inbox 不存在（例如還沒掛載）時回傳 ok=false。
func (w *Watcher) list() (files []file, truncated, ok bool, err error) {
	root, err := os.OpenRoot(w.Inbox)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, false, nil
		}
		return nil, false, false, err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// 單一目錄或檔案讀不到（權限、剛好被刪除）：略過，不讓整輪失敗。
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if p == "." {
			return nil
		}
		name := d.Name()
		depth := strings.Count(p, "/") + 1
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || depth >= maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		// symlink 與其他非一般檔案一律略過（不追蹤，也不會被當成影音）。
		if !d.Type().IsRegular() || skipName(name) {
			return nil
		}
		if len(files) >= maxFiles {
			truncated = true
			return fs.SkipAll
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		files = append(files, file{rel: p, size: fi.Size(), mtime: fi.ModTime()})
		return nil
	})
	if err != nil {
		return nil, false, false, err
	}
	slices.SortFunc(files, func(a, b file) int {
		if c := a.mtime.Compare(b.mtime); c != 0 {
			return c
		}
		return strings.Compare(a.rel, b.rel)
	})
	return files, truncated, true, nil
}

// scan 掃描一次。呼叫端持有 w.mu。
func (w *Watcher) scan(ctx context.Context) error {
	files, truncated, ok, err := w.list()
	if err != nil {
		return fmt.Errorf("掃描 inbox：%w", err)
	}
	if !ok {
		return nil
	}
	if w.seen == nil {
		w.seen, w.warned = map[string]seenFile{}, map[string]string{}
	}
	now := w.Clock.Now()

	present := make(map[string]bool, len(files))
	for _, f := range files {
		present[f.rel] = true
		s, known := w.seen[f.rel]
		switch {
		case !known:
			// 第一次看到：修改時間早於現在就從修改時間起算（啟動前就放進來的檔案可以直接穩定）。
			since := now
			if f.mtime.Before(now) {
				since = f.mtime
			}
			w.seen[f.rel] = seenFile{size: f.size, mtime: f.mtime, since: since}
		case s.size != f.size || !s.mtime.Equal(f.mtime):
			w.seen[f.rel] = seenFile{size: f.size, mtime: f.mtime, since: now}
		}
	}

	entries, err := w.Store.Read().InboxEntries(ctx)
	if err != nil {
		return fmt.Errorf("讀取 inbox 紀錄：%w", err)
	}
	// 檔案消失：刪除判定紀錄與記憶體狀態。清單被截斷時不刪，避免誤刪沒掃到的檔案。
	if !truncated {
		for rel := range w.seen {
			if !present[rel] {
				delete(w.seen, rel)
				delete(w.warned, rel)
			}
		}
		for rel := range entries {
			if !present[rel] {
				if err := w.Store.Write().DeleteInboxEntry(ctx, rel); err != nil {
					return fmt.Errorf("刪除 inbox 紀錄：%w", err)
				}
			}
		}
	}

	inbox, err := w.Files.Resolve(w.Inbox)
	if err != nil {
		return fmt.Errorf("解析 inbox 路徑：%w", err)
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := w.seen[f.rel]
		if w.Clock.Now().Sub(s.since) < StableFor {
			continue
		}
		if e, done := entries[f.rel]; done && e.Size == f.size && e.MtimeNS == f.mtime.UnixNano() {
			continue
		}
		if err := w.process(ctx, inbox, f); err != nil {
			return err
		}
	}
	return nil
}

// warnOnce 對同一個檔案的同一個狀態只記一次暫時性失敗的 log。
func (w *Watcher) warnOnce(f file, msg string, err error) {
	fp := fmt.Sprintf("%s|%d|%d", msg, f.size, f.mtime.UnixNano())
	if w.warned[f.rel] == fp {
		return
	}
	w.warned[f.rel] = fp
	w.log().Warn(msg, "path", f.rel, "error", err)
}

// process 處理一個穩定的檔案。回傳錯誤只限 ctx 取消與資料庫錯誤；檔案本身的問題記 log、下一輪重試。
func (w *Watcher) process(ctx context.Context, inbox string, f file) error {
	abs, err := w.Files.Resolve(filepath.Join(inbox, filepath.FromSlash(f.rel)))
	if err != nil {
		w.warnOnce(f, "inbox 檔案的路徑不在允許的範圍內，略過", err)
		return nil
	}

	// (2) ffprobe 篩選：不是影音的檔案留在原處並記錄原因（ING-R11）。
	if _, err := w.Media.Probe(ctx, abs); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, media.ErrNotMedia) {
			// 工具不存在、逾時：不寫紀錄，下一輪重試。
			w.warnOnce(f, "inbox 檔案無法探測，稍後重試", err)
			return nil
		}
		return w.record(ctx, f, "", store.InboxNotMedia, "沒有音訊或影片："+probeDetail(err), "")
	}

	// (3) 雜湊；算完再確認檔案沒有變動，變動就重新計時。
	sum, err := w.hash(ctx, abs)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, fs.ErrNotExist) {
			w.warnOnce(f, "inbox 檔案無法讀取，稍後重試", err)
		}
		return nil
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return nil // 檔案消失：下一輪會刪除紀錄
	}
	if fi.Size() != f.size || !fi.ModTime().Equal(f.mtime) {
		w.seen[f.rel] = seenFile{size: fi.Size(), mtime: fi.ModTime(), since: w.Clock.Now()}
		return nil
	}

	// (4) 新增：inbox 遇到 tombstone 不建立影音、也不移除 tombstone（DEL-R11）。
	out, err := w.Ingester.IngestLocal(ctx, pipeline.LocalTarget{
		Key: f.rel, SourcePath: abs, SHA256: sum, Origin: store.OriginInbox,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		w.warnOnce(f, "inbox 檔案新增失敗，稍後重試", err)
		return nil
	}
	return w.record(ctx, f, sum, out.State, stateReason(out), out.Item.ID)
}

// record (5) 寫入判定紀錄並記 log。
func (w *Watcher) record(ctx context.Context, f file, sum, state, reason, itemID string) error {
	err := w.Store.Write().UpsertInboxEntry(ctx, store.InboxEntry{
		Path: f.rel, Size: f.size, MtimeNS: f.mtime.UnixNano(), SHA256: sum, State: state, Reason: reason,
		ItemID: itemID, DecidedAt: w.Clock.Now(),
	})
	if err != nil {
		return fmt.Errorf("寫入 inbox 紀錄：%w", err)
	}
	delete(w.warned, f.rel)
	w.log().Info("inbox 檔案已判定", "path", f.rel, "state", state, "reason", reason)
	return nil
}

// hash 計算檔案內容的 sha256，期間檢查 ctx 取消。
func (w *Watcher) hash(ctx context.Context, abs string) (string, error) {
	f, _, err := w.Files.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ctxReader 讓長時間的讀取可以被取消。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// probeDetail 取出 ffprobe 的錯誤說明（最後一行 stderr）。
func probeDetail(err error) string {
	var ce *errclass.Error
	if errors.As(err, &ce) && ce.Detail != "" {
		return ce.Detail
	}
	return err.Error()
}

// stateReason 是各判定的原因說明（寫入 inbox_entries.reason，ING-R11）。
func stateReason(out pipeline.LocalOutcome) string {
	switch out.State {
	case store.InboxDuplicate:
		return "內容與既有影音相同，沒有重複新增。"
	case store.InboxTombstoned:
		return "這個檔案曾經永久刪除，不會自動重新新增；要加回來請手動新增。"
	case store.InboxInTrash:
		return "對應的影音在垃圾桶中；還原後才能使用。"
	case store.InboxConflict:
		return "同一個路徑的檔案內容已被換掉，不會自動重新處理。"
	}
	return ""
}
