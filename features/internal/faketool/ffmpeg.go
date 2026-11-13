package faketool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const fakeFFVersion = "7.1-notesteme-fake"

// runFFmpeg 支援 -version／--version，以及 -buildconf（可加 -hide_banner）；其他參數結束碼 2。
// -buildconf 的內容是否含 --enable-libass 由 State.FFmpegNoLibass 決定。
func runFFmpeg(e env, args []string) int {
	_ = AppendCall(e.root, Call{Tool: e.tool, Argv: e.argv, Cwd: e.cwd})
	if len(args) == 1 && (args[0] == "-version" || args[0] == "--version") {
		fmt.Fprintf(e.stdout, "ffmpeg version %s\n", fakeFFVersion)
		return 0
	}
	if isBuildconf(args) {
		st, err := ReadState(e.root)
		if err != nil {
			fmt.Fprintf(e.stderr, "fake ffmpeg: %v\n", err)
			return 2
		}
		fmt.Fprint(e.stdout, "configuration:\n  --enable-gpl\n  --enable-libx264\n")
		if !st.FFmpegNoLibass {
			fmt.Fprint(e.stdout, "  --enable-libass\n")
		}
		fmt.Fprint(e.stdout, "  --enable-shared\n")
		return 0
	}
	return usageError(e, "unsupported arguments %q", strings.Join(args, " "))
}

// isBuildconf 判斷參數是否為 -buildconf 或 -hide_banner -buildconf。
func isBuildconf(args []string) bool {
	if len(args) == 2 && args[0] == "-hide_banner" {
		args = args[1:]
	}
	return len(args) == 1 && args[0] == "-buildconf"
}

// runFFprobe 支援 -version，以及 -of/-print_format json 讀取假媒體標記並回傳 duration。
// 可接受的輔助旗標：-v <level>、-hide_banner、-show_format、-show_streams、-show_entries <x>。
func runFFprobe(e env, args []string) int {
	_ = AppendCall(e.root, Call{Tool: e.tool, Argv: e.argv, Cwd: e.cwd})
	if len(args) == 1 && (args[0] == "-version" || args[0] == "--version") {
		fmt.Fprintf(e.stdout, "ffprobe version %s\n", fakeFFVersion)
		return 0
	}
	var (
		jsonOut, showStreams bool
		input                string
	)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-hide_banner", "-show_format":
		case "-show_streams":
			showStreams = true
		case "-v", "-show_entries", "-of", "-print_format":
			if i+1 >= len(args) {
				return usageError(e, "option %s requires an argument", a)
			}
			i++
			if a == "-of" || a == "-print_format" {
				if args[i] != "json" {
					return usageError(e, "unsupported output format %q (only json)", args[i])
				}
				jsonOut = true
			}
		default:
			if strings.HasPrefix(a, "-") || input != "" {
				return usageError(e, "unsupported argument %q", a)
			}
			input = a
		}
	}
	if !jsonOut || input == "" {
		return usageError(e, "requires -of json and an input file")
	}
	path := input
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.cwd, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: No such file or directory\n", input)
		return 1
	}
	_, dur, ok := ParseMediaMarker(b)
	if !ok {
		fmt.Fprintf(e.stderr, "%s: Invalid data found when processing input\n", input)
		return 1
	}
	out := map[string]any{
		"format": map[string]any{
			"filename":    input,
			"format_name": "fake",
			"duration":    fmt.Sprintf("%d.000000", dur),
		},
	}
	if showStreams {
		out["streams"] = []map[string]any{{"index": 0, "codec_type": "audio", "duration": fmt.Sprintf("%d.000000", dur)}}
	}
	enc, _ := json.MarshalIndent(out, "", "    ")
	fmt.Fprintln(e.stdout, string(enc))
	return 0
}
