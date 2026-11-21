package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// fakeMedia 是 ffmpeg 與 ffprobe 的替身（實作 toolexec.Runner），依 argv 判斷要做什麼並記錄呼叫。
// 它只模擬 internal/media 會送出的 argv，認不得的 argv 讓測試失敗。
type fakeMedia struct {
	t  *testing.T
	mu sync.Mutex
	// calls 記錄每次呼叫：工具名稱與 argv。
	calls []fakeMediaCall

	// duration 是 ffprobe 回報的長度（秒）。
	duration float64
	// streams 是 ffprobe 回報的串流 JSON 陣列內容；空代表一條音訊串流。
	streams string
	// probeFail 非 nil 時，對這些路徑（完整比對）的 ffprobe 以 exit 1 結束。
	probeFail map[string]string
	// prepSize 是前處理輸出的大小（byte）；預設 1 KiB。
	prepSize int64
	// chunkSize 回傳切塊輸出的大小；nil 代表 1 KiB。
	chunkSize func(startS, lenS float64) int64
	// silences 是前處理後音訊的靜音（絕對時間）。
	silences []media.Silence
	// subtitles 是 ExtractSubtitle 寫出的內容，依串流 index。
	subtitles map[int]string
	// ffmpegExit 非 0 時 ffmpeg 以該結束碼結束（-version 除外）。
	ffmpegExit int
}

type fakeMediaCall struct {
	Tool string
	Args []string
}

// newFakeMedia 建立 fakeMedia 與用它組成的 media.Tool。
func newFakeMedia(t *testing.T) (*fakeMedia, *media.Tool) {
	t.Helper()
	f := &fakeMedia{t: t, duration: 600, prepSize: 1024}
	dir := t.TempDir()
	cfg := map[string]string{}
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		cfg[name] = p
	}
	return f, media.New(f, toolexec.Resolver{Config: cfg, Vars: map[string]string{}})
}

// ffmpegCalls 回傳 ffmpeg 的呼叫（不含 -version）。
func (f *fakeMedia) ffmpegCalls() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][]string
	for _, c := range f.calls {
		if c.Tool == "ffmpeg" && !hasArg(c.Args, "-version") {
			out = append(out, c.Args)
		}
	}
	return out
}

// countKind 回傳某一種 ffmpeg 呼叫的次數：prepare、chunk、silence、subtitle。
func (f *fakeMedia) countKind(kind string) int {
	n := 0
	for _, a := range f.ffmpegCalls() {
		if ffmpegKind(a) == kind {
			n++
		}
	}
	return n
}

func hasArg(args []string, a string) bool {
	for _, x := range args {
		if x == a {
			return true
		}
	}
	return false
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func ffmpegKind(args []string) string {
	switch {
	case hasArg(args, "-af"):
		return "silence"
	case hasArg(args, "-c:s"):
		return "subtitle"
	case hasArg(args, "libopus") && hasArg(args, "-ss"):
		return "chunk"
	case hasArg(args, "libopus"):
		return "prepare"
	}
	return "unknown"
}

func (f *fakeMedia) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tool := filepath.Base(c.Path)
	f.calls = append(f.calls, fakeMediaCall{Tool: tool, Args: c.Args})
	if hasArg(c.Args, "-version") {
		return toolexec.Result{Stdout: []byte(tool + " version 6.1.1 Copyright (c) the FFmpeg developers\n")}, nil
	}
	if tool == "ffprobe" {
		in := c.Args[len(c.Args)-1]
		if msg, ok := f.probeFail[in]; ok {
			return toolexec.Result{ExitCode: 1, Stderr: []byte(msg + "\n")}, nil
		}
		streams := f.streams
		if streams == "" {
			streams = `{"index":0,"codec_type":"audio","codec_name":"aac"}`
		}
		out := fmt.Sprintf(`{"streams":[%s],"format":{"duration":"%v","format_name":"mov,mp4"}}`, streams, f.duration)
		return toolexec.Result{Stdout: []byte(out)}, nil
	}
	if f.ffmpegExit != 0 {
		return toolexec.Result{ExitCode: f.ffmpegExit, Stderr: []byte("boom\n")}, nil
	}
	out := c.Args[len(c.Args)-1]
	switch ffmpegKind(c.Args) {
	case "prepare":
		f.write(out, f.prepSize)
	case "chunk":
		start, _ := strconv.ParseFloat(argAfter(c.Args, "-ss"), 64)
		length, _ := strconv.ParseFloat(argAfter(c.Args, "-t"), 64)
		size := int64(1024)
		if f.chunkSize != nil {
			size = f.chunkSize(start, length)
		}
		f.write(out, size)
	case "silence":
		from, _ := strconv.ParseFloat(argAfter(c.Args, "-ss"), 64)
		length, _ := strconv.ParseFloat(argAfter(c.Args, "-t"), 64)
		var sb strings.Builder
		for _, s := range f.silences {
			if s.EndS < from || s.StartS > from+length {
				continue
			}
			fmt.Fprintf(&sb, "[silencedetect @ 0x1] silence_start: %v\n[silencedetect @ 0x1] silence_end: %v | silence_duration: %v\n",
				s.StartS-from, s.EndS-from, s.EndS-s.StartS)
		}
		return toolexec.Result{Stderr: []byte(sb.String())}, nil
	case "subtitle":
		idx, _ := strconv.Atoi(strings.TrimPrefix(argAfter(c.Args, "-map"), "0:"))
		body, ok := f.subtitles[idx]
		if !ok {
			return toolexec.Result{ExitCode: 1, Stderr: []byte("Stream map '0:" + strconv.Itoa(idx) + "' matches no streams.\n")}, nil
		}
		if err := os.WriteFile(out, []byte(body), 0o600); err != nil {
			f.t.Fatal(err)
		}
	default:
		f.t.Fatalf("fakeMedia 不認得的 ffmpeg argv：%q", c.Args)
	}
	return toolexec.Result{}, nil
}

// write 建立大小為 size 的檔案（稀疏）。
func (f *fakeMedia) write(path string, size int64) {
	fh, err := os.Create(path)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	if err := fh.Truncate(size); err != nil {
		f.t.Fatal(err)
	}
}
