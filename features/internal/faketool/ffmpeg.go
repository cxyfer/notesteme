package faketool

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cxyfer/notesteme/features/internal/fakeframes"
)

const fakeFFVersion = "7.1-notesteme-fake"

// markerReadLimit 是讀取標記時最多讀的位元組數：媒體檔可能是很大的稀疏檔，不能整個讀進記憶體。
const markerReadLimit = 64 << 10

// runFFmpeg 支援：
//   - -version／--version，-buildconf（可加 -hide_banner）；
//   - ASR 前處理、切塊、靜音偵測、抽字幕四種呼叫，參數照 local-design 第 4 節的 argv 契約逐項檢查
//     （選項不在契約內、缺少必要選項或值不同，都結束碼 2）；
//   - 樣式預覽（OUT-R9）：用 libass 把工作目錄的 s.ass 疊在畫面上，輸出一張 PNG，見 ffmpegPreview。
//
// 輸入檔是假媒體檔（標記，見 media.go）；其他內容視為不是媒體，結束碼 1。
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
	fa, err := parseFFmpegArgs(args)
	if err != nil {
		return usageError(e, "%v (argv %q)", err, strings.Join(args, " "))
	}
	switch fa.mode {
	case ffSilence:
		return ffmpegSilence(e, fa)
	case ffSubtitle:
		return ffmpegSubtitle(e, fa)
	case ffPreview:
		return ffmpegPreview(e, fa)
	case ffScenes:
		return ffmpegScenes(e, fa)
	case ffFrame:
		return ffmpegFrame(e, fa)
	default:
		return ffmpegTranscode(e, fa)
	}
}

// isBuildconf 判斷參數是否為 -buildconf 或 -hide_banner -buildconf。
func isBuildconf(args []string) bool {
	if len(args) == 2 && args[0] == "-hide_banner" {
		args = args[1:]
	}
	return len(args) == 1 && args[0] == "-buildconf"
}

// ---- ffmpeg 的參數 ----

type ffMode int

const (
	ffTranscode ffMode = iota // ASR 前處理或切塊（有 -ss／-t）
	ffSilence                 // -af silencedetect
	ffSubtitle                // -c:s srt
	ffPreview                 // -vf ass=…：樣式預覽
	ffScenes                  // -vf select='gt(scene,T)',…：場景偵測（FRM-R2）
	ffFrame                   // -vf scale=W:H -q:v 2：擷取一個畫面（FRM-R8）
)

// ffArgs 是解析後的 ffmpeg 參數。
type ffArgs struct {
	mode   ffMode
	vals   map[string]string // 帶值的選項
	bools  map[string]bool   // 不帶值的選項
	pos    map[string]int    // 選項出現的位置（檢查 -ss／-t 在 -i 之前）
	output string
}

var (
	ffValueOpts = map[string]bool{
		"-v": true, "-protocol_whitelist": true, "-format_whitelist": true, "-ss": true, "-t": true, "-i": true,
		"-map": true, "-ac": true, "-ar": true, "-c:a": true, "-b:a": true, "-application": true, "-f": true,
		"-af": true, "-c:s": true, "-vf": true, "-frames:v": true, "-update": true, "-c:v": true,
		"-q:v": true,
	}
	ffBoolOpts = map[string]bool{
		"-nostdin": true, "-hide_banner": true, "-nostats": true, "-vn": true, "-sn": true, "-dn": true, "-y": true,
		"-an": true,
	}
	// 每種模式必要的選項（值固定的列在 want，只要求存在的列在 present）。
	ffTranscodeWant = map[string]string{
		"-v": "error", "-protocol_whitelist": "file", "-map": "0:a:0", "-ac": "1", "-ar": "16000",
		"-c:a": "libopus", "-b:a": "32k", "-application": "voip", "-f": "ogg",
	}
	ffTranscodeBools = []string{"-nostdin", "-hide_banner", "-vn", "-sn", "-dn", "-y"}

	ffSilenceWant = map[string]string{
		"-v": "info", "-protocol_whitelist": "file", "-af": "silencedetect=noise=-35dB:d=0.3", "-f": "null",
	}
	ffSilenceBools = []string{"-nostdin", "-hide_banner", "-nostats"}

	ffSubtitleWant = map[string]string{
		"-v": "error", "-protocol_whitelist": "file", "-c:s": "srt", "-f": "srt",
	}
	ffSubtitleBools = []string{"-nostdin", "-hide_banner", "-y"}

	whitelistName = regexp.MustCompile(`^[a-z0-9_]+$`)
	subtitleMap   = regexp.MustCompile(`^0:(\d+)$`)
)

// parseFFmpegArgs 解析並檢查 ffmpeg 的參數；不符合任何一種契約時回傳說明原因的錯誤。
func parseFFmpegArgs(args []string) (*ffArgs, error) {
	fa := &ffArgs{vals: map[string]string{}, bools: map[string]bool{}, pos: map[string]int{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case ffBoolOpts[a]:
			if fa.bools[a] {
				return nil, fmt.Errorf("duplicate option %s", a)
			}
			fa.bools[a], fa.pos[a] = true, i
		case ffValueOpts[a]:
			if i+1 >= len(args) {
				return nil, fmt.Errorf("option %s requires an argument", a)
			}
			if _, dup := fa.vals[a]; dup {
				return nil, fmt.Errorf("duplicate option %s", a)
			}
			i++
			fa.vals[a], fa.pos[a] = args[i], i-1
		case a == "-" || !strings.HasPrefix(a, "-"):
			if fa.output != "" {
				return nil, fmt.Errorf("more than one output %q", a)
			}
			fa.output = a
		default:
			return nil, fmt.Errorf("unsupported option %s", a)
		}
	}
	if fa.output == "" {
		return nil, fmt.Errorf("missing output file")
	}
	if _, ok := fa.vals["-i"]; !ok {
		return nil, fmt.Errorf("missing -i")
	}
	var want map[string]string
	var bools []string
	switch {
	case strings.HasPrefix(fa.vals["-vf"], "select="):
		fa.mode = ffScenes
		if err := fa.checkFrames(); err != nil {
			return nil, err
		}
		return fa, nil
	case strings.HasPrefix(fa.vals["-vf"], "scale="):
		fa.mode = ffFrame
		if err := fa.checkFrames(); err != nil {
			return nil, err
		}
		return fa, nil
	case fa.vals["-vf"] != "":
		fa.mode = ffPreview
		if err := fa.checkPreview(); err != nil {
			return nil, err
		}
		return fa, nil
	case fa.vals["-af"] != "":
		fa.mode, want, bools = ffSilence, ffSilenceWant, ffSilenceBools
	case fa.vals["-c:s"] != "":
		fa.mode, want, bools = ffSubtitle, ffSubtitleWant, ffSubtitleBools
	default:
		fa.mode, want, bools = ffTranscode, ffTranscodeWant, ffTranscodeBools
	}
	if err := fa.check(want, bools); err != nil {
		return nil, err
	}
	return fa, nil
}

// check 檢查必要選項與值，以及白名單；並確認沒有契約以外的選項。
func (fa *ffArgs) check(want map[string]string, bools []string) error {
	// 契約以外的選項：該模式沒有列出，也不是可選的（-ss、-t、-i、-map、-format_whitelist 另外處理）。
	allowed := map[string]bool{"-i": true, "-format_whitelist": true, "-ss": true, "-t": true}
	for k := range want {
		allowed[k] = true
	}
	switch fa.mode {
	case ffSubtitle:
		allowed["-map"] = true
	}
	for k := range fa.vals {
		if !allowed[k] {
			return fmt.Errorf("option %s is not part of the %s argv contract", k, fa.modeName())
		}
	}
	for k := range fa.bools {
		if !slices.Contains(bools, k) {
			return fmt.Errorf("option %s is not part of the %s argv contract", k, fa.modeName())
		}
	}
	for k, v := range want {
		got, ok := fa.vals[k]
		if !ok {
			return fmt.Errorf("%s requires %s %s", fa.modeName(), k, v)
		}
		if got != v {
			return fmt.Errorf("%s must be %q, got %q", k, v, got)
		}
	}
	for _, b := range bools {
		if !fa.bools[b] {
			return fmt.Errorf("%s requires %s", fa.modeName(), b)
		}
	}
	// 白名單：-protocol_whitelist 已在上面確認是 file；-format_whitelist 每種模式都必填（含靜音偵測），且不含 hls、concat 等。
	if wl, ok := fa.vals["-format_whitelist"]; ok {
		if err := checkFormatWhitelist(wl); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("%s requires -format_whitelist", fa.modeName())
	}
	_, hasSS := fa.vals["-ss"]
	_, hasT := fa.vals["-t"]
	switch fa.mode {
	case ffTranscode:
		if hasSS != hasT {
			return fmt.Errorf("-ss and -t must be given together")
		}
	case ffSilence:
		if !hasSS || !hasT {
			return fmt.Errorf("silencedetect requires -ss and -t")
		}
		if fa.output != "-" {
			return fmt.Errorf("silencedetect output must be -")
		}
	case ffSubtitle:
		if hasSS || hasT {
			return fmt.Errorf("subtitle extraction does not take -ss or -t")
		}
		if !subtitleMap.MatchString(fa.vals["-map"]) {
			return fmt.Errorf("-map must be 0:<stream index>, got %q", fa.vals["-map"])
		}
	}
	for _, k := range []string{"-ss", "-t"} {
		if v, ok := fa.vals[k]; ok {
			if f, err := strconv.ParseFloat(v, 64); err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("%s %q is not a non-negative number", k, v)
			}
			// -ss、-t 必須在 -i 之前（input option）：從原始檔快速定位，而不是輸出後再裁切。
			if fa.pos[k] > fa.pos["-i"] {
				return fmt.Errorf("%s must come before -i", k)
			}
		}
	}
	return nil
}

func (fa *ffArgs) modeName() string {
	switch fa.mode {
	case ffSilence:
		return "silencedetect"
	case ffSubtitle:
		return "subtitle extraction"
	case ffPreview:
		return "preview"
	case ffScenes:
		return "scene detection"
	case ffFrame:
		return "frame extraction"
	}
	return "transcode"
}

// checkFormatWhitelist 檢查 -format_whitelist 的清單：非空、每項是小寫名稱，且不含 hls 與 concat
// （它們可以參照其他檔案或網址，見 local-design 第 4 節）。
func checkFormatWhitelist(wl string) error {
	if wl == "" {
		return fmt.Errorf("-format_whitelist is empty")
	}
	for _, f := range strings.Split(wl, ",") {
		if !whitelistName.MatchString(f) {
			return fmt.Errorf("-format_whitelist has a bad entry %q", f)
		}
		if slices.Contains([]string{"hls", "concat", "image2", "tee", "data", "lavfi"}, f) {
			return fmt.Errorf("-format_whitelist must not include %s", f)
		}
	}
	return nil
}

func (fa *ffArgs) number(k string) float64 {
	f, _ := strconv.ParseFloat(fa.vals[k], 64)
	return f
}

// ---- 輸入與輸出 ----

// absPath 把相對路徑換成相對工作目錄的絕對路徑。
func absPath(e env, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.cwd, p)
}

// readMedia 讀取假媒體檔的標記與檔案大小。err 為檔案不存在等 IO 錯誤；不是標記時 ok 為 false。
func readMedia(path string) (m Media, size int64, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return Media{}, 0, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Media{}, 0, false, err
	}
	b, err := io.ReadAll(io.LimitReader(f, markerReadLimit))
	if err != nil {
		return Media{}, 0, false, err
	}
	m, ok = ParseMedia(b)
	return m, fi.Size(), ok, nil
}

// openInput 讀取 ffmpeg 的輸入。失敗時已輸出錯誤訊息，回傳的結束碼是 1。
func openInput(e env, in string) (Media, bool) {
	m, _, ok, err := readMedia(absPath(e, in))
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: No such file or directory\n", in)
		return Media{}, false
	}
	if !ok {
		fmt.Fprintf(e.stderr, "%s: Invalid data found when processing input\n", in)
		return Media{}, false
	}
	return m, true
}

// writeMedia 寫出標記；size 大於標記長度時以 Truncate 補成稀疏檔，檔案大小剛好等於 size。
func writeMedia(e env, out string, m Media, size int64) error {
	path := absPath(e, out)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	marker := FormatMedia(m)
	if err := os.WriteFile(path, []byte(marker), 0o644); err != nil {
		return err
	}
	if size > int64(len(marker)) {
		return os.Truncate(path, size)
	}
	return nil
}

// ffmpegTranscode 是 ASR 前處理（轉成單聲道 16 kHz opus）與切塊（-ss／-t 在 -i 之前，從原始檔重新編碼）。
// 輸出保留 item、duration、silences；大小等於 compressed（切塊依時長比例）。
func ffmpegTranscode(e env, fa *ffArgs) int {
	in := fa.vals["-i"]
	m, ok := openInput(e, in)
	if !ok {
		return 1
	}
	hasAudio := false
	for _, s := range m.EffectiveStreams() {
		if s.Type == StreamAudio {
			hasAudio = true
		}
	}
	if !hasAudio {
		fmt.Fprintln(e.stderr, "Stream map '0:a:0' matches no streams.")
		return 1
	}
	out := Media{
		Item:    m.Item,
		Streams: []MediaStream{{Type: StreamAudio, Codec: "opus"}},
	}
	size := m.Compressed
	if _, chunk := fa.vals["-ss"]; chunk {
		start, length := fa.number("-ss"), fa.number("-t")
		if start >= m.Duration {
			fmt.Fprintln(e.stderr, "Output file is empty, nothing was encoded")
			return 1
		}
		length = math.Min(length, m.Duration-start)
		out.Duration, out.Offset, out.HasOffset = length, start, true
		size = 0
		if m.Compressed > 0 && m.Duration > 0 {
			size = int64(math.Round(float64(m.Compressed) * length / m.Duration))
		}
	} else {
		out.Duration, out.Silences = m.Duration, m.Silences
	}
	out.Compressed = size
	if err := writeMedia(e, fa.output, out, size); err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", fa.output, err)
		return 1
	}
	return 0
}

// ffmpegSilence 模擬 silencedetect：把標記中落在視窗 [-ss, -ss+-t] 內的靜音以真實 ffmpeg 的格式
// 輸出到 stderr，時間相對視窗起點。跨過視窗邊界的靜音在邊界截斷。
func ffmpegSilence(e env, fa *ffArgs) int {
	in := fa.vals["-i"]
	m, ok := openInput(e, in)
	if !ok {
		return 1
	}
	from, length := fa.number("-ss"), fa.number("-t")
	to := math.Min(from+length, m.Duration)
	fmt.Fprintf(e.stderr, "Input #0, ogg, from '%s':\n", in)
	for _, s := range m.Silences {
		start, end := math.Max(s.Start, from), math.Min(s.End, to)
		if end <= start {
			continue
		}
		fmt.Fprintf(e.stderr, "[silencedetect @ 0x55d0c8a1b2c0] silence_start: %s\n", silenceTime(start-from))
		fmt.Fprintf(e.stderr, "[silencedetect @ 0x55d0c8a1b2c0] silence_end: %s | silence_duration: %s\n",
			silenceTime(end-from), silenceTime(end-start))
	}
	return 0
}

// silenceTime 以最多六位小數輸出，去掉多餘的 0（浮點誤差不外露）。
func silenceTime(x float64) string {
	return strconv.FormatFloat(math.Round(x*1e6)/1e6, 'f', -1, 64)
}

// ffmpegSubtitle 抽出指定串流的字幕：把串流指定的 fixture（features/fixtures/local/ 內）複製到輸出。
// 串流不存在、不是字幕或沒有 fixture（點陣字幕）時失敗，結束碼 1。
func ffmpegSubtitle(e env, fa *ffArgs) int {
	in := fa.vals["-i"]
	m, ok := openInput(e, in)
	if !ok {
		return 1
	}
	idx, _ := strconv.Atoi(subtitleMap.FindStringSubmatch(fa.vals["-map"])[1])
	streams := m.EffectiveStreams()
	if idx >= len(streams) || streams[idx].Type != StreamSubtitle {
		fmt.Fprintf(e.stderr, "Stream map '%s' matches no streams.\n", fa.vals["-map"])
		return 1
	}
	fixture := streams[idx].Fixture
	if fixture == "" {
		fmt.Fprintln(e.stderr, "Subtitle encoding currently only possible from text to text or bitmap to bitmap")
		return 1
	}
	st, err := ReadState(e.root)
	if err != nil || st.FixturesDir == "" {
		fmt.Fprintf(e.stderr, "fake ffmpeg: state has no fixtures_dir (%v)\n", err)
		return 2
	}
	b, err := os.ReadFile(filepath.Join(st.FixturesDir, "local", filepath.Clean("/"+fixture)))
	if err != nil {
		fmt.Fprintf(e.stderr, "fake ffmpeg: subtitle fixture %q: %v\n", fixture, err)
		return 1
	}
	if err := writeFile(e, fa.output, b); err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", fa.output, err)
		return 1
	}
	return 0
}

// ---- 樣式預覽 ----

var (
	// 沒有影片時的合成畫布：color=c=0xRRGGBB:s=寬x高:r=1:d=1。
	previewCanvas = regexp.MustCompile(`^color=c=0x[0-9a-fA-F]{6}:s=(\d{2,4})x(\d{2,4}):r=1:d=1$`)

	ffPreviewWant  = map[string]string{"-v": "error", "-vf": "ass=s.ass", "-frames:v": "1", "-update": "1", "-c:v": "png"}
	ffPreviewBools = []string{"-nostdin", "-hide_banner", "-an", "-sn", "-dn", "-y"}
)

// checkPreview 檢查預覽的 argv 契約。兩種輸入：
//   - 合成畫布：-f lavfi -i color=…，不帶白名單與 -ss（lavfi 不讀任何檔案）；
//   - 影片檔：-protocol_whitelist file、-format_whitelist、-ss 在 -i 之前、-i 是絕對路徑。
//
// 輸出一律是相對的 out.png，字幕檔一律是相對的 s.ass（工作目錄是預覽專用的暫存目錄）。
func (fa *ffArgs) checkPreview() error {
	for k, v := range ffPreviewWant {
		if got, ok := fa.vals[k]; !ok || got != v {
			return fmt.Errorf("preview requires %s %s (got %q)", k, v, got)
		}
	}
	for _, b := range ffPreviewBools {
		if !fa.bools[b] {
			return fmt.Errorf("preview requires %s", b)
		}
	}
	for k := range fa.bools {
		if !slices.Contains(ffPreviewBools, k) {
			return fmt.Errorf("option %s is not part of the preview argv contract", k)
		}
	}
	if fa.output != "out.png" {
		return fmt.Errorf("preview output must be out.png, got %q", fa.output)
	}
	in := fa.vals["-i"]
	if f, ok := fa.vals["-f"]; ok {
		if f != "lavfi" {
			return fmt.Errorf("preview -f must be lavfi, got %q", f)
		}
		if !previewCanvas.MatchString(in) {
			return fmt.Errorf("preview canvas %q is not color=c=0x…:s=WxH:r=1:d=1", in)
		}
		for _, k := range []string{"-protocol_whitelist", "-format_whitelist", "-ss", "-t"} {
			if _, ok := fa.vals[k]; ok {
				return fmt.Errorf("preview canvas does not take %s", k)
			}
		}
		return nil
	}
	if fa.vals["-protocol_whitelist"] != "file" {
		return fmt.Errorf("preview of a video requires -protocol_whitelist file")
	}
	wl, ok := fa.vals["-format_whitelist"]
	if !ok {
		return fmt.Errorf("preview of a video requires -format_whitelist")
	}
	if err := checkFormatWhitelist(wl); err != nil {
		return err
	}
	if !filepath.IsAbs(in) {
		return fmt.Errorf("preview input %q must be an absolute path", in)
	}
	ss, ok := fa.vals["-ss"]
	if !ok {
		return fmt.Errorf("preview of a video requires -ss")
	}
	if f, err := strconv.ParseFloat(ss, 64); err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("-ss %q is not a non-negative number", ss)
	}
	if fa.pos["-ss"] > fa.pos["-i"] {
		return fmt.Errorf("-ss must come before -i")
	}
	if _, ok := fa.vals["-t"]; ok {
		return fmt.Errorf("preview does not take -t")
	}
	return nil
}

// ffmpegPreview 模擬 libass 預覽：讀工作目錄的 s.ass（要有 [V4+ Styles]、Style 與 Dialogue），輸出一張真正的 PNG。
// 沒有 libass（State.FFmpegNoLibass）時和真實的 ffmpeg 一樣找不到 ass 濾鏡，結束碼 1。
// 影片檔輸入要是有影像串流的假媒體檔，-ss 不能超過長度。
func ffmpegPreview(e env, fa *ffArgs) int {
	st, err := ReadState(e.root)
	if err != nil {
		fmt.Fprintf(e.stderr, "fake ffmpeg: %v\n", err)
		return 2
	}
	if st.FFmpegNoLibass {
		fmt.Fprintln(e.stderr, "No such filter: 'ass'")
		return 1
	}
	w, h := 1280, 720
	if m := previewCanvas.FindStringSubmatch(fa.vals["-i"]); m != nil {
		w, _ = strconv.Atoi(m[1])
		h, _ = strconv.Atoi(m[2])
	} else {
		media, ok := openInput(e, fa.vals["-i"])
		if !ok {
			return 1
		}
		hasVideo := false
		for _, s := range media.EffectiveStreams() {
			hasVideo = hasVideo || s.Type == StreamVideo
		}
		if !hasVideo {
			fmt.Fprintln(e.stderr, "Output file does not contain any stream")
			return 1
		}
		if fa.number("-ss") >= media.Duration {
			fmt.Fprintln(e.stderr, "Output file is empty, nothing was encoded")
			return 1
		}
	}
	ass, err := os.ReadFile(absPath(e, "s.ass"))
	if err != nil {
		fmt.Fprintln(e.stderr, "s.ass: No such file or directory")
		return 1
	}
	if err := checkPreviewASS(string(ass)); err != nil {
		fmt.Fprintf(e.stderr, "s.ass: %v\n", err)
		return 1
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		fmt.Fprintf(e.stderr, "fake ffmpeg: %v\n", err)
		return 1
	}
	if err := writeFile(e, fa.output, buf.Bytes()); err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", fa.output, err)
		return 1
	}
	return 0
}

// checkPreviewASS 檢查字幕檔夠不夠 libass 渲染：有 [V4+ Styles]、Format 與 Style 行，以及至少一行 Dialogue。
func checkPreviewASS(ass string) error {
	section, styleFormat, styles, dialogues := "", false, 0, 0
	for _, l := range strings.Split(strings.TrimPrefix(ass, "\uFEFF"), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "["):
			section = strings.ToLower(l)
		case section == "[v4+ styles]" && strings.HasPrefix(l, "Format:"):
			styleFormat = true
		case section == "[v4+ styles]" && strings.HasPrefix(l, "Style:"):
			styles++
		case section == "[events]" && strings.HasPrefix(l, "Dialogue:"):
			dialogues++
		}
	}
	switch {
	case !styleFormat || styles == 0:
		return fmt.Errorf("no [V4+ Styles] with a Format line and a Style")
	case dialogues == 0:
		return fmt.Errorf("no Dialogue in [Events]")
	}
	return nil
}

// ---- ffprobe ----

// formatNameFor 依副檔名回傳 ffprobe 的 format_name（真實 ffprobe 的寫法）。
func formatNameFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4a", ".mov", ".m4v":
		return "mov,mp4,m4a,3gp,3g2,mj2"
	case ".mkv", ".webm":
		return "matroska,webm"
	case ".mp3":
		return "mp3"
	case ".ogg", ".opus":
		return "ogg"
	case ".wav":
		return "wav"
	case ".flac":
		return "flac"
	}
	return "fake"
}

// runFFprobe 支援 -version，以及：
//
//	-v <level> -hide_banner -protocol_whitelist file -format_whitelist <FW> -show_format -show_streams -of json <path>
//
// -protocol_whitelist file 與 -format_whitelist（不含 hls、concat）是必要的：程式必須限制 ffprobe 能讀的來源。
// -print_format、-show_entries 是 ffprobe 的別名與輔助旗標，照舊接受。
// 輸入是假媒體檔（標記）；依標記回報串流（含 tags.language 與 disposition）與 format.duration。
func runFFprobe(e env, args []string) int {
	_ = AppendCall(e.root, Call{Tool: e.tool, Argv: e.argv, Cwd: e.cwd})
	if len(args) == 1 && (args[0] == "-version" || args[0] == "--version") {
		fmt.Fprintf(e.stdout, "ffprobe version %s\n", fakeFFVersion)
		return 0
	}
	var (
		jsonOut, showStreams bool
		input                string
		protocolWL, formatWL string
		haveProtocol, haveFW bool
	)
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "-hide_banner", "-show_format":
		case "-show_streams":
			showStreams = true
		case "-v", "-show_entries", "-of", "-print_format", "-protocol_whitelist", "-format_whitelist":
			if i+1 >= len(args) {
				return usageError(e, "option %s requires an argument", a)
			}
			i++
			switch a {
			case "-of", "-print_format":
				if args[i] != "json" {
					return usageError(e, "unsupported output format %q (only json)", args[i])
				}
				jsonOut = true
			case "-protocol_whitelist":
				protocolWL, haveProtocol = args[i], true
			case "-format_whitelist":
				formatWL, haveFW = args[i], true
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
	if !haveProtocol || protocolWL != "file" {
		return usageError(e, "requires -protocol_whitelist file (got %q)", protocolWL)
	}
	if !haveFW {
		return usageError(e, "requires -format_whitelist")
	}
	if err := checkFormatWhitelist(formatWL); err != nil {
		return usageError(e, "%v", err)
	}
	path := absPath(e, input)
	m, size, ok, err := readMedia(path)
	if err != nil {
		fmt.Fprintf(e.stderr, "%s: No such file or directory\n", input)
		return 1
	}
	if !ok {
		fmt.Fprintf(e.stderr, "%s: Invalid data found when processing input\n", input)
		return 1
	}
	dur := fmt.Sprintf("%.6f", m.Duration)
	streams := m.EffectiveStreams()
	out := map[string]any{
		"format": map[string]any{
			"filename":    input,
			"nb_streams":  len(streams),
			"format_name": formatNameFor(input),
			"duration":    dur,
			"size":        strconv.FormatInt(size, 10),
		},
	}
	if showStreams {
		list := make([]map[string]any, 0, len(streams))
		seenType := map[string]bool{}
		for i, s := range streams {
			st := map[string]any{
				"index":      i,
				"codec_type": s.Type,
				"codec_name": s.Codec,
				"duration":   dur,
			}
			// 每種類型的第一條串流是預設串流（多數封裝格式的慣例）。
			isDefault := 0
			if !seenType[s.Type] {
				isDefault, seenType[s.Type] = 1, true
			}
			st["disposition"] = map[string]int{"default": isDefault, "forced": 0, "attached_pic": 0}
			if s.Lang != "" {
				st["tags"] = map[string]string{"language": s.Lang}
			}
			if s.Type == StreamVideo && m.Width > 0 {
				st["width"], st["height"] = m.Width, m.Height
			}
			list = append(list, st)
		}
		out["streams"] = list
	}
	enc, _ := json.MarshalIndent(out, "", "    ")
	fmt.Fprintln(e.stdout, string(enc))
	return 0
}

// ---- 關鍵畫面（FRM） ----

var (
	scenesFilter = regexp.MustCompile(`^select='gt\(scene,([0-9]+(?:\.[0-9]+)?)\)',showinfo,metadata=print$`)
	scaleFilter  = regexp.MustCompile(`^scale=(\d{1,4}):(\d{1,4})$`)

	ffScenesWant  = map[string]string{"-v": "info", "-protocol_whitelist": "file", "-f": "null"}
	ffScenesBools = []string{"-nostdin", "-hide_banner", "-nostats", "-an", "-sn", "-dn"}
	ffFrameWant   = map[string]string{"-v": "error", "-protocol_whitelist": "file", "-frames:v": "1", "-q:v": "2", "-f": "mjpeg"}
	ffFrameBools  = []string{"-nostdin", "-hide_banner", "-an", "-sn", "-dn", "-y"}
)

// checkFrames 檢查場景偵測與擷取的 argv 契約（internal/media 的 DetectScenes、ExtractFrame）：
// 必要選項與值固定、沒有契約以外的選項、白名單、輸入是絕對路徑；擷取的 -ss 在 -i 之前，輸出是絕對路徑的 .jpg。
func (fa *ffArgs) checkFrames() error {
	want, bools := ffScenesWant, ffScenesBools
	if fa.mode == ffFrame {
		want, bools = ffFrameWant, ffFrameBools
	}
	allowed := map[string]bool{"-i": true, "-format_whitelist": true, "-vf": true}
	if fa.mode == ffFrame {
		allowed["-ss"] = true
	}
	for k := range want {
		allowed[k] = true
	}
	for k := range fa.vals {
		if !allowed[k] {
			return fmt.Errorf("option %s is not part of the %s argv contract", k, fa.modeName())
		}
	}
	for k := range fa.bools {
		if !slices.Contains(bools, k) {
			return fmt.Errorf("option %s is not part of the %s argv contract", k, fa.modeName())
		}
	}
	for k, v := range want {
		if fa.vals[k] != v {
			return fmt.Errorf("%s requires %s %s (got %q)", fa.modeName(), k, v, fa.vals[k])
		}
	}
	for _, b := range bools {
		if !fa.bools[b] {
			return fmt.Errorf("%s requires %s", fa.modeName(), b)
		}
	}
	wl, ok := fa.vals["-format_whitelist"]
	if !ok {
		return fmt.Errorf("%s requires -format_whitelist", fa.modeName())
	}
	if err := checkFormatWhitelist(wl); err != nil {
		return err
	}
	if !filepath.IsAbs(fa.vals["-i"]) {
		return fmt.Errorf("%s input %q must be an absolute path", fa.modeName(), fa.vals["-i"])
	}
	if fa.mode == ffScenes {
		if !scenesFilter.MatchString(fa.vals["-vf"]) {
			return fmt.Errorf("scene detection filter %q is not select='gt(scene,T)',showinfo,metadata=print", fa.vals["-vf"])
		}
		if fa.output != "-" {
			return fmt.Errorf("scene detection output must be -")
		}
		return nil
	}
	if !scaleFilter.MatchString(fa.vals["-vf"]) {
		return fmt.Errorf("frame filter %q is not scale=W:H", fa.vals["-vf"])
	}
	ss, ok := fa.vals["-ss"]
	if !ok {
		return fmt.Errorf("frame extraction requires -ss")
	}
	if f, err := strconv.ParseFloat(ss, 64); err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return fmt.Errorf("-ss %q is not a non-negative number", ss)
	}
	if fa.pos["-ss"] > fa.pos["-i"] {
		return fmt.Errorf("-ss must come before -i")
	}
	if !filepath.IsAbs(fa.output) || !strings.HasSuffix(fa.output, ".jpg") {
		return fmt.Errorf("frame output %q must be an absolute .jpg path", fa.output)
	}
	return nil
}

// videoInput 讀取有影像串流的輸入，以及這部影片在狀態中登記的畫面。失敗時已輸出錯誤訊息。
func videoInput(e env, in string) (Media, fakeframes.Video, bool) {
	m, ok := openInput(e, in)
	if !ok {
		return Media{}, fakeframes.Video{}, false
	}
	hasVideo := false
	for _, s := range m.EffectiveStreams() {
		hasVideo = hasVideo || s.Type == StreamVideo
	}
	if !hasVideo {
		fmt.Fprintln(e.stderr, "Output file does not contain any stream")
		return Media{}, fakeframes.Video{}, false
	}
	st, err := ReadState(e.root)
	if err != nil {
		fmt.Fprintf(e.stderr, "fake ffmpeg: %v\n", err)
		return Media{}, fakeframes.Video{}, false
	}
	var scenes []fakeframes.Scene
	if v := st.Videos[m.Item]; v != nil {
		scenes = v.Scenes
	}
	return m, fakeframes.NewVideo(scenes), true
}

// ffmpegScenes 模擬場景偵測：把分數大於門檻的畫面以 showinfo 的格式寫到 stderr（fakeframes.ShowinfoLog）。
func ffmpegScenes(e env, fa *ffArgs) int {
	_, v, ok := videoInput(e, fa.vals["-i"])
	if !ok {
		return 1
	}
	t, _ := strconv.ParseFloat(scenesFilter.FindStringSubmatch(fa.vals["-vf"])[1], 64)
	fmt.Fprintf(e.stderr, "Input #0, mov,mp4,m4a,3gp,3g2,mj2, from '%s':\n", fa.vals["-i"])
	fmt.Fprint(e.stderr, v.ShowinfoLog(t))
	return 0
}

// ffmpegFrame 模擬擷取：-ss 時間的畫面是純色 JPEG（fakeframes.WriteFrame），尺寸照 scale。-ss 超過片長時失敗。
func ffmpegFrame(e env, fa *ffArgs) int {
	m, v, ok := videoInput(e, fa.vals["-i"])
	if !ok {
		return 1
	}
	at := fa.number("-ss")
	if at >= m.Duration {
		fmt.Fprintln(e.stderr, "Output file is empty, nothing was encoded")
		return 1
	}
	sm := scaleFilter.FindStringSubmatch(fa.vals["-vf"])
	w, _ := strconv.Atoi(sm[1])
	h, _ := strconv.Atoi(sm[2])
	if w < 1 || h < 1 {
		fmt.Fprintln(e.stderr, "Invalid scale size")
		return 1
	}
	if err := v.WriteFrame(fa.output, at, w, h); err != nil {
		fmt.Fprintf(e.stderr, "%s: %v\n", fa.output, err)
		return 1
	}
	return 0
}
