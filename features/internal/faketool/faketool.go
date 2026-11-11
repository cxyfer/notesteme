// Package faketool 讓測試 binary 本身改扮 yt-dlp、ffmpeg、ffprobe（ADR-0015）。
//
// 使用方式：
//
//	func TestMain(m *testing.M) {
//	    faketool.Dispatch() // 若是以假工具的名稱被執行，就在這裡結束
//	    os.Exit(m.Run())
//	}
//
// 每個場景的 bin/ 以 Install 放三個指向測試 binary 的 symlink，PATH 指到 bin/。
// 假工具讀 <root>/fake/state.json（WriteState 寫入），並把每次呼叫附加到
// <root>/fake/calls.jsonl（ReadCalls 讀取）。遇到未知的參數一律以結束碼 2 結束，
// 及早發現程式與替身的介面不一致。
package faketool

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 假工具的名稱。
const (
	YtDlp   = "yt-dlp"
	FFmpeg  = "ffmpeg"
	FFprobe = "ffprobe"
)

var toolNames = []string{YtDlp, FFmpeg, FFprobe}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// toolFromArg0 依 argv[0] 的檔名判斷要扮演哪個工具；不是假工具時回傳空字串。
func toolFromArg0(arg0 string) string {
	base := filepath.Base(arg0)
	if runtime.GOOS == "windows" {
		base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	}
	for _, n := range toolNames {
		if base == n {
			return n
		}
	}
	return ""
}

// Dispatch 在 TestMain 的第一行呼叫：若 argv[0] 的檔名是 yt-dlp、ffmpeg 或 ffprobe
// （Windows 為 .exe），就執行對應的假工具並以其結束碼呼叫 os.Exit；否則直接回傳。
func Dispatch() {
	tool := toolFromArg0(os.Args[0])
	if tool == "" {
		return
	}
	cwd, _ := os.Getwd()
	root := filepath.Dir(selfDir())
	// 找不到場景狀態代表不是在場景內被執行：不建立任何檔案，直接失敗。
	if err := requireScenario(root); err != nil {
		fmt.Fprintf(os.Stderr, "fake %s: %v\n", tool, err)
		os.Exit(2)
	}
	os.Exit(run(env{
		tool:   tool,
		root:   root,
		cwd:    cwd,
		argv:   os.Args,
		stdout: os.Stdout,
		stderr: os.Stderr,
	}))
}

// requireScenario 確認 root 下有場景狀態檔（<root>/fake/state.json）。
// 假工具被誤放到場景外（例如 PATH 指到別處）時，不能在陌生的目錄建立 fake/ 或 calls.jsonl。
func requireScenario(root string) error {
	p := statePath(root)
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("找不到場景狀態檔 %s（%v）；假工具只能在驗收場景內執行，不會建立任何檔案", p, err)
	}
	return nil
}

// selfDir 回傳假工具 symlink 所在的目錄（<root>/bin）。
// 刻意使用 os.Args[0] 而不是 os.Executable：後者會解開 symlink，找不到場景目錄。
// 以 PATH 查找而被執行時 argv[0] 只有名稱，此時用 PATH 找回完整路徑。
func selfDir() string {
	a := os.Args[0]
	if !strings.ContainsRune(a, filepath.Separator) {
		if p, err := exec.LookPath(a); err == nil {
			a = p
		}
	}
	abs, err := filepath.Abs(a)
	if err != nil {
		abs = a
	}
	return filepath.Dir(abs)
}

// Install 在 binDir 建立 yt-dlp、ffmpeg、ffprobe 三個 symlink，指向測試 binary。
// 已存在的同名檔案會被取代。
func Install(binDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	for _, n := range toolNames {
		link := filepath.Join(binDir, exeName(n))
		if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(exe, link); err != nil {
			return fmt.Errorf("faketool: symlink %s: %w", link, err)
		}
	}
	return nil
}

// env 是一次假工具執行的環境，方便在程序內直接測試 run。
type env struct {
	tool   string
	root   string // 場景根目錄（其下有 fake/）
	cwd    string
	argv   []string // 含 argv[0]
	stdout io.Writer
	stderr io.Writer
}

func run(e env) int {
	args := e.argv[1:]
	switch e.tool {
	case YtDlp:
		return runYtDlp(e, args)
	case FFmpeg:
		return runFFmpeg(e, args)
	case FFprobe:
		return runFFprobe(e, args)
	}
	fmt.Fprintf(e.stderr, "faketool: unknown tool %q\n", e.tool)
	return 2
}

// usageError 回報未知或不支援的參數，結束碼 2。
func usageError(e env, format string, a ...any) int {
	fmt.Fprintf(e.stderr, "fake %s: %s (the fake does not support this; update features/internal/faketool if the program legitimately needs it)\n",
		e.tool, fmt.Sprintf(format, a...))
	return 2
}

// 假媒體檔的第一行標記，fake ASR 與假 ffprobe 靠它辨識是哪部影音、多長。

// MediaMarker 回傳假媒體檔的內容：一行 "NOTESTEME-FAKE-MEDIA item=<id> duration=<秒>"。
func MediaMarker(item string, durationSec int) string {
	return fmt.Sprintf("NOTESTEME-FAKE-MEDIA item=%s duration=%d\n", item, durationSec)
}

// ParseMediaMarker 解析假媒體檔的內容（只看第一行）。
func ParseMediaMarker(b []byte) (item string, durationSec int, ok bool) {
	line := string(b)
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	const prefix = "NOTESTEME-FAKE-MEDIA "
	if !strings.HasPrefix(line, prefix) {
		return "", 0, false
	}
	var gotItem, gotDur bool
	for _, f := range strings.Fields(strings.TrimPrefix(line, prefix)) {
		k, v, found := strings.Cut(f, "=")
		if !found {
			continue
		}
		switch k {
		case "item":
			item, gotItem = v, true
		case "duration":
			if _, err := fmt.Sscanf(v, "%d", &durationSec); err == nil {
				gotDur = true
			}
		}
	}
	return item, durationSec, gotItem && gotDur
}
