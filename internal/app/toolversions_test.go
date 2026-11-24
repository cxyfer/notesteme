package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

// versionRunner 依檔案內容回報版本，並記錄每個路徑被執行的次數。
type versionRunner struct {
	mu    sync.Mutex
	calls map[string]int
	delay time.Duration
	fail  bool
}

func (r *versionRunner) Run(ctx context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	r.mu.Lock()
	r.calls[c.Path]++
	r.mu.Unlock()
	if r.delay > 0 {
		select {
		case <-time.After(r.delay):
		case <-ctx.Done():
			return toolexec.Result{}, ctx.Err()
		}
	}
	if r.fail {
		return toolexec.Result{ExitCode: 1}, nil
	}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return toolexec.Result{}, err
	}
	return toolexec.Result{Stdout: b}, nil
}

func (r *versionRunner) count(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[path]
}

func writeTool(t *testing.T, path, out string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(out), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestParseToolVersion(t *testing.T) {
	cases := []struct{ name, out, want string }{
		{"yt-dlp", "2026.11.01\n", "2026.11.01"},
		{"ffmpeg", "ffmpeg version 7.1-notesteme-fake Copyright (c) 2000-2024\nbuilt with gcc\n", "7.1-notesteme-fake"},
		{"ffprobe", "ffprobe version 6.1.1-3ubuntu5 Copyright\n", "6.1.1-3ubuntu5"},
		{"deno", "deno 2.1.4 (stable, release, x86_64-unknown-linux-gnu)\nv8 13.0\n", "2.1.4"},
		{"deno", "\n\n", ""},
		{"yt-dlp", "something unexpected here\n", ""},
		{"yt-dlp", strings.Repeat("9", maxVersionLen+1), ""},
	}
	for _, c := range cases {
		if got := parseToolVersion(c.name, c.out); got != c.want {
			t.Errorf("parseToolVersion(%q, %q) = %q，預期 %q", c.name, c.out, got, c.want)
		}
	}
}

// 有快取時不再執行；symlink 改指向新的檔案（yt-dlp 更新、回退）時重新執行。
func TestToolVersionsCacheFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	v1 := filepath.Join(dir, "2026.11.01", "yt-dlp")
	v2 := filepath.Join(dir, "2026.11.10", "yt-dlp")
	writeTool(t, v1, "2026.11.01\n")
	writeTool(t, v2, "2026.11.10\n")
	link := filepath.Join(dir, "yt-dlp")
	if err := os.Symlink(v1, link); err != nil {
		t.Skipf("不能建立 symlink：%v", err)
	}
	r := &versionRunner{calls: map[string]int{}}
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	cache := newToolVersions(r, func() time.Time { return now })
	locs := map[string]toolexec.Location{"yt-dlp": {Path: link}}
	ctx := context.Background()

	if got := cache.Versions(ctx, locs)["yt-dlp"]; got != "2026.11.01" {
		t.Fatalf("第一次 = %q", got)
	}
	if got := cache.Versions(ctx, locs)["yt-dlp"]; got != "2026.11.01" || r.count(link) != 1 {
		t.Fatalf("快取：版本 %q、執行 %d 次", got, r.count(link))
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(v2, link); err != nil {
		t.Fatal(err)
	}
	if got := cache.Versions(ctx, locs)["yt-dlp"]; got != "2026.11.10" || r.count(link) != 2 {
		t.Fatalf("換版本後：版本 %q、執行 %d 次", got, r.count(link))
	}
	// 成功的結果過了 versionOKTTL 才重新執行。
	now = now.Add(versionOKTTL + time.Second)
	cache.Versions(ctx, locs)
	if r.count(link) != 3 {
		t.Fatalf("過期後執行 %d 次，預期 3", r.count(link))
	}
}

// 失敗也快取 versionFailTTL；不能執行的工具不執行；結果中沒有不知道的版本。
func TestToolVersionsCachesFailures(t *testing.T) {
	dir := t.TempDir()
	ff := filepath.Join(dir, "ffmpeg")
	writeTool(t, ff, "garbage")
	r := &versionRunner{calls: map[string]int{}, fail: true}
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	cache := newToolVersions(r, func() time.Time { return now })
	locs := map[string]toolexec.Location{
		"ffmpeg": {Path: ff},
		"deno":   {Path: filepath.Join(dir, "deno"), Err: os.ErrNotExist},
	}
	ctx := context.Background()
	for range 3 {
		if got := cache.Versions(ctx, locs); len(got) != 0 {
			t.Fatalf("結果 = %v，預期空的", got)
		}
	}
	if r.count(ff) != 1 || r.count(filepath.Join(dir, "deno")) != 0 {
		t.Fatalf("ffmpeg 執行 %d 次、deno %d 次", r.count(ff), r.count(filepath.Join(dir, "deno")))
	}
	now = now.Add(versionFailTTL + time.Second)
	cache.Versions(ctx, locs)
	if r.count(ff) != 2 {
		t.Fatalf("失敗的快取過期後執行 %d 次，預期 2", r.count(ff))
	}
}

// 沒有快取時同時執行所有工具，整體不超過一個逾時期限。
func TestToolVersionsProbeInParallelWithinTimeout(t *testing.T) {
	dir := t.TempDir()
	locs := map[string]toolexec.Location{}
	for _, n := range []string{"yt-dlp", "ffmpeg", "ffprobe", "deno"} {
		p := filepath.Join(dir, n)
		writeTool(t, p, "1.0\n")
		locs[n] = toolexec.Location{Path: p}
	}
	r := &versionRunner{calls: map[string]int{}, delay: time.Hour}
	cache := newToolVersions(r, time.Now)
	cache.timeout = 100 * time.Millisecond
	start := time.Now()
	if got := cache.Versions(context.Background(), locs); len(got) != 0 {
		t.Fatalf("逾時的結果 = %v", got)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("花了 %v：沒有同時執行或沒有逾時", d)
	}
}
