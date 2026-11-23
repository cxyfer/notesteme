// Package diskusage 統計資料目錄的磁碟用量（LIB-R22）：/data 與其下 media、artifacts、uploads 的檔案大小總和，
// 以及 /data 所在檔案系統的可用空間。只讀取，不刪除任何檔案。
//
// 不跟隨符號連結；結果快取 TTL（以注入的 clock 判斷）；一次統計最多 Timeout，逾時時回傳上一次的結果，
// 從來沒有統計完成時各數字是 nil。同時只會有一次統計在進行，其他呼叫等它（最多到它的期限）。
package diskusage

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

// 預設的快取時間與單次統計的上限（LIB-R22）。
const (
	DefaultTTL     = 5 * time.Minute
	DefaultTimeout = 30 * time.Second
)

// Usage 是一次統計的結果；nil 代表沒有數字（從來沒有統計完成，或無法取得可用空間）。
type Usage struct {
	DataBytes      *int64
	MediaBytes     *int64
	ArtifactsBytes *int64
	UploadsBytes   *int64
	FreeBytes      *int64
	MeasuredAt     *time.Time
}

// Meter 統計一個資料目錄並快取結果。零值不可用，請用 New。並行安全。
type Meter struct {
	dir     string
	clock   clock.Clock
	TTL     time.Duration
	Timeout time.Duration
	// walk 與 free 可以在測試中替換。
	walk func(ctx context.Context, dir string) (sizes, error)
	free func(dir string) (int64, bool)

	mu       sync.Mutex
	last     *Usage
	inflight chan struct{}
}

// New 建立統計 dir 的 Meter。
func New(dir string, clk clock.Clock) *Meter {
	return &Meter{dir: dir, clock: clk, TTL: DefaultTTL, Timeout: DefaultTimeout, walk: walk, free: freeBytes}
}

// Get 回傳磁碟用量：快取還沒過期時直接回傳；否則統計一次（或等進行中的那次），逾時或 ctx 結束時回傳上一次的結果。
func (m *Meter) Get(ctx context.Context) Usage {
	m.mu.Lock()
	if m.last != nil && m.clock.Now().Sub(*m.last.MeasuredAt) < m.TTL {
		u := *m.last
		m.mu.Unlock()
		return u
	}
	done := m.inflight
	if done == nil {
		done = make(chan struct{})
		m.inflight = done
		// 統計不受單一請求取消影響：請求放棄等待時，統計仍做完並更新快取（上限 Timeout）。
		go m.measure(done)
	}
	m.mu.Unlock()

	timer := time.NewTimer(m.Timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	case <-ctx.Done():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		return Usage{}
	}
	return *m.last
}

// measure 做一次統計；完成時更新快取，逾時則保留上一次的結果。
func (m *Meter) measure(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), m.Timeout)
	defer cancel()
	s, err := m.walk(ctx, m.dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	defer close(done)
	m.inflight = nil
	if err != nil {
		return
	}
	at := m.clock.Now()
	u := Usage{DataBytes: ptr(s.data), MediaBytes: ptr(s.media), ArtifactsBytes: ptr(s.artifacts), UploadsBytes: ptr(s.uploads), MeasuredAt: &at}
	if f, ok := m.free(m.dir); ok {
		u.FreeBytes = ptr(f)
	}
	m.last = &u
}

func ptr[T any](v T) *T { return &v }

// sizes 是一次走訪的結果。
type sizes struct{ data, media, artifacts, uploads int64 }

// walk 加總 dir 下一般檔案的大小；不跟隨符號連結（WalkDir 不進入連結指向的目錄，連結本身不是一般檔案）。
// 讀不到的項目略過；ctx 結束時回傳錯誤，這次的結果不採用。
func walk(ctx context.Context, dir string) (sizes, error) {
	var s sizes
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		n := info.Size()
		s.data += n
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		switch top {
		case "media":
			s.media += n
		case "artifacts":
			s.artifacts += n
		case "uploads":
			s.uploads += n
		}
		return nil
	})
	return s, err
}
