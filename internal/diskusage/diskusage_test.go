package diskusage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
)

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
		t.Fatal(err)
	}
}

func val(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

// LIB-R22：/data 與 media、artifacts、uploads 的大小；不跟隨符號連結；不刪除任何檔案。
func TestGetSizes(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "notesteme.db"), 100)
	write(t, filepath.Join(dir, "media", "a", "v.mp4"), 1000)
	write(t, filepath.Join(dir, "artifacts", "a", "t.json"), 20)
	write(t, filepath.Join(dir, "artifacts", "b", "t.json"), 30)
	write(t, filepath.Join(dir, "uploads", "x", "f.mp3"), 7)
	write(t, filepath.Join(dir, "mediaX", "not-media"), 3)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "big"), 1<<20)
	if err := os.Symlink(outside, filepath.Join(dir, "media", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "big"), filepath.Join(dir, "uploads", "file-link")); err != nil {
		t.Fatal(err)
	}

	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	m := New(dir, clk)
	u := m.Get(context.Background())
	if val(u.DataBytes) != 1160 || val(u.MediaBytes) != 1000 || val(u.ArtifactsBytes) != 50 || val(u.UploadsBytes) != 7 {
		t.Errorf("用量 = data %d media %d artifacts %d uploads %d", val(u.DataBytes), val(u.MediaBytes), val(u.ArtifactsBytes), val(u.UploadsBytes))
	}
	if u.MeasuredAt == nil || !u.MeasuredAt.Equal(clk.Now()) {
		t.Errorf("measured_at = %v", u.MeasuredAt)
	}
	if u.FreeBytes == nil || *u.FreeBytes <= 0 {
		t.Errorf("free = %v", u.FreeBytes)
	}
	if _, err := os.Stat(filepath.Join(outside, "big")); err != nil {
		t.Error("不能刪除任何檔案")
	}
}

// 快取 5 分鐘（注入的 clock）；過期後重新統計。
func TestGetCaches(t *testing.T) {
	dir := t.TempDir()
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	m := New(dir, clk)
	var calls atomic.Int32
	m.walk = func(ctx context.Context, d string) (sizes, error) {
		calls.Add(1)
		return walk(ctx, d)
	}
	m.Get(context.Background())
	write(t, filepath.Join(dir, "x"), 5)
	clk.Advance(DefaultTTL - time.Second)
	if u := m.Get(context.Background()); val(u.DataBytes) != 0 || calls.Load() != 1 {
		t.Errorf("快取期間 = %d（%d 次）", val(u.DataBytes), calls.Load())
	}
	clk.Advance(time.Second)
	if u := m.Get(context.Background()); val(u.DataBytes) != 5 || calls.Load() != 2 {
		t.Errorf("過期後 = %d（%d 次）", val(u.DataBytes), calls.Load())
	}
}

// 逾時：從來沒有統計完成時各數字是 nil；之後逾時回傳上一次的結果。同時只有一次統計。
func TestGetTimeout(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC))
	m := New(t.TempDir(), clk)
	m.Timeout = 50 * time.Millisecond
	release := make(chan struct{})
	var calls atomic.Int32
	m.walk = func(ctx context.Context, _ string) (sizes, error) {
		calls.Add(1)
		select {
		case <-release:
			return sizes{data: 9}, nil
		case <-ctx.Done():
			return sizes{}, ctx.Err()
		}
	}
	m.free = func(string) (int64, bool) { return 0, false }
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if u := m.Get(context.Background()); u.DataBytes != nil || u.MeasuredAt != nil {
				t.Errorf("沒有統計完成 = %+v", u)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("同時只有一次統計：%d", calls.Load())
	}

	// 統計成功一次，過期後再逾時：回傳上一次的結果。
	go func() { close(release) }()
	time.Sleep(10 * time.Millisecond)
	u := m.Get(context.Background())
	if val(u.DataBytes) != 9 || u.FreeBytes != nil {
		t.Fatalf("成功 = %+v", u)
	}
	clk.Advance(DefaultTTL)
	m.walk = func(ctx context.Context, _ string) (sizes, error) {
		<-ctx.Done()
		return sizes{}, ctx.Err()
	}
	if u := m.Get(context.Background()); val(u.DataBytes) != 9 {
		t.Errorf("逾時 = %+v", u)
	}
}

// 呼叫端取消時不等統計，回傳上一次的結果（這裡是 nil）。
func TestGetCancelled(t *testing.T) {
	m := New(t.TempDir(), clock.NewFake(time.Now()))
	m.walk = func(ctx context.Context, _ string) (sizes, error) {
		<-ctx.Done()
		return sizes{}, errors.New("stopped")
	}
	m.Timeout = time.Second
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if u := m.Get(ctx); u.DataBytes != nil {
		t.Errorf("取消 = %+v", u)
	}
}

// 走訪中 ctx 結束：回傳錯誤，不採用部分結果。
func TestWalkStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a"), 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := walk(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if s, err := walk(context.Background(), filepath.Join(dir, "missing")); err != nil || s.data != 0 {
		t.Errorf("不存在的目錄 = %+v, %v", s, err)
	}
	if _, ok := freeBytes(filepath.Join(dir, "missing")); ok {
		t.Error("不存在的路徑沒有可用空間")
	}
}
