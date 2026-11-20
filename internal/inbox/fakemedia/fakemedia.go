// Package fakemedia 是測試用的假 ffprobe（只給單元測試使用）：依檔案內容決定探測結果，不執行任何外部程式。
//
// 內容以 "VIDEO" 開頭是影片（影像加音訊），以 "AUDIO" 開頭是音訊，其他不是影音（ffprobe 結束碼 1）。
package fakemedia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// Runner 實作 toolexec.Runner，記錄每次 ffprobe 探測的路徑。
type Runner struct {
	mu    sync.Mutex
	calls []string
	// Err 非 nil 時，所有探測都以這個錯誤失敗（模擬工具逾時、無法執行）。
	err error
	// OnProbe 在每次探測開始時呼叫（不持有鎖），可以用來模擬探測期間檔案被改動。
	OnProbe func(path string)
}

// Calls 回傳至今探測過的路徑。
func (r *Runner) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// SetErr 設定（或用 nil 清除）所有探測的失敗。
func (r *Runner) SetErr(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

// Run 實作 toolexec.Runner：只認 ffprobe 的呼叫，路徑是最後一個參數。
func (r *Runner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	if len(c.Args) == 0 {
		return toolexec.Result{}, errors.New("fakemedia: 沒有參數")
	}
	path := c.Args[len(c.Args)-1]
	r.mu.Lock()
	r.calls = append(r.calls, path)
	err, hook := r.err, r.OnProbe
	r.mu.Unlock()
	if hook != nil {
		hook(path)
	}
	if err != nil {
		return toolexec.Result{}, err
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		return toolexec.Result{ExitCode: 1, Stderr: []byte(rerr.Error() + "\n")}, nil
	}
	switch {
	case bytes.HasPrefix(b, []byte("VIDEO")):
		return toolexec.Result{Stdout: []byte(`{"streams":[{"index":0,"codec_type":"video","codec_name":"h264"},` +
			`{"index":1,"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"60.0","format_name":"mov,mp4"}}`)}, nil
	case bytes.HasPrefix(b, []byte("AUDIO")):
		return toolexec.Result{Stdout: []byte(`{"streams":[{"index":0,"codec_type":"audio","codec_name":"aac"}],` +
			`"format":{"duration":"30.0","format_name":"mov,mp4"}}`)}, nil
	}
	return toolexec.Result{ExitCode: 1, Stderr: []byte("x: Invalid data found when processing input\n")}, nil
}

// New 建立指向假 Runner 的 media.Tool；ffmpeg 與 ffprobe 指向暫存目錄中的假執行檔（只用來通過路徑解析）。
func New(t testing.TB) (*media.Tool, *Runner) {
	t.Helper()
	dir := t.TempDir()
	cfg := map[string]string{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg[name] = p
	}
	r := &Runner{}
	return media.New(r, toolexec.Resolver{Config: cfg, Vars: map[string]string{}}), r
}
