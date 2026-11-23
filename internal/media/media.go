// Package media 以 ffprobe 與 ffmpeg 處理本機影音檔（ING-05）：探測串流、抽出內嵌字幕、
// 為語音轉錄壓縮與切塊、偵測靜音。
//
// 所有指令的 argv 都是固定的（見各函式）：只開放 file protocol 與有限的容器格式，
// 避免 HLS、concat 之類的 demuxer 讀取檔案以外的資源。路徑必須是絕對路徑，避免被當成選項。
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 各工具呼叫的逾時。
const (
	probeTimeout    = 2 * time.Minute
	subtitleTimeout = 5 * time.Minute
	encodeTimeout   = 30 * time.Minute
	silenceTimeout  = 10 * time.Minute
	versionTimeout  = 30 * time.Second
	previewTimeout  = 2 * time.Minute
)

// formatWhitelist 是 -format_whitelist 的清單：排除 hls、concat、image2、tee、data、lavfi 等
// 會讀取其他資源或不是檔案格式的 demuxer。
const formatWhitelist = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,mp3,ogg,wav,flac,aac,avi,mpegts,asf,srt,webvtt,ass," +
	"flv,mpeg,aiff,ac3,eac3,amr,caf,w64,wv"

// ErrNotMedia 表示檔案不是影音：ffprobe 無法讀取，或沒有音訊與影像串流。
// Probe 回傳的錯誤包裝了它，並帶 errclass permanent；用 errors.Is 辨識。
var ErrNotMedia = errors.New("沒有音訊或影片")

const msgNotMedia = "這個檔案沒有音訊或影片，無法處理。"

const (
	msgNoFFmpeg  = "無法執行 ffmpeg，請確認已安裝（可執行 notesteme doctor）。"
	msgNoFFprobe = "無法執行 ffprobe，請確認已安裝（可執行 notesteme doctor）。"
)

// Stream 是容器中的一條串流。
type Stream struct {
	Index int
	// Type 是 audio、video、subtitle、data 或 attachment。
	Type  string
	Codec string
	// Lang 是 BCP-47 語言碼；und 或空白為 ""。
	Lang        string
	Title       string
	Default     bool
	Forced      bool
	AttachedPic bool
	// Width、Height 是影像串流的尺寸（ffprobe 沒有回報時為 0），FRM-R8 依它決定擷取尺寸。
	Width, Height int
}

// Info 是 ffprobe 的結果。
type Info struct {
	DurationS float64
	Format    string
	Streams   []Stream
}

// HasAV 回報是否有音訊串流，或非封面圖的影像串流。
func (i *Info) HasAV() bool {
	for _, s := range i.Streams {
		if s.Type == "audio" || (s.Type == "video" && !s.AttachedPic) {
			return true
		}
	}
	return false
}

// Kind 回傳 "video"（有非封面圖的影像串流）或 "audio"。
func (i *Info) Kind() string {
	for _, s := range i.Streams {
		if s.Type == "video" && !s.AttachedPic {
			return "video"
		}
	}
	return "audio"
}

// VideoSize 回傳第一條非封面圖影像串流的尺寸；沒有影像串流或沒有尺寸時 ok 為 false。
func (i *Info) VideoSize() (w, h int, ok bool) {
	for _, s := range i.Streams {
		if s.Type == "video" && !s.AttachedPic {
			return s.Width, s.Height, s.Width > 0 && s.Height > 0
		}
	}
	return 0, 0, false
}

// Silence 是一段靜音，時間為絕對秒數（Silences 已加上視窗起點）。
type Silence struct {
	StartS, EndS float64
}

// Tool 呼叫 ffmpeg 與 ffprobe。
type Tool struct {
	Runner toolexec.Runner
	// Tools 尋找 ffmpeg 與 ffprobe 的位置（OPS-R8）；與 app.ToolResolver 相同。
	Tools toolexec.Resolver
}

// New 建立 Tool。
func New(r toolexec.Runner, tools toolexec.Resolver) *Tool { return &Tool{Runner: r, Tools: tools} }

// run 解析並執行工具。找不到工具為 needs_action；執行失敗為 retryable；被取消回傳 ctx 的錯誤。
// 非 0 的結束碼不是錯誤，由呼叫端依用途分類。
func (t *Tool) run(ctx context.Context, spec toolexec.Spec, timeout time.Duration, args []string) (toolexec.Result, error) {
	return t.runIn(ctx, spec, timeout, "", args)
}

// runIn 同 run，另外指定工作目錄（空字串是呼叫者目前的目錄）。
func (t *Tool) runIn(ctx context.Context, spec toolexec.Spec, timeout time.Duration, dir string, args []string) (toolexec.Result, error) {
	bin, err := t.Tools.Resolve(spec)
	if err != nil {
		msg, action := msgNoFFmpeg, "安裝 ffmpeg"
		if spec.Name == toolexec.FFprobe.Name {
			msg, action = msgNoFFprobe, "安裝 ffprobe"
		}
		return toolexec.Result{}, &errclass.Error{
			Class: errclass.ClassNeedsAction, Source: spec.Name, Err: err, Message: msg, Actions: []string{action},
		}
	}
	res, err := t.Runner.Run(ctx, toolexec.Cmd{Path: bin, Args: args, Dir: dir, Timeout: timeout})
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, &errclass.Error{Class: errclass.ClassRetryable, Source: spec.Name, Err: err}
	}
	return res, nil
}

// runFFmpeg 執行 ffmpeg，結束碼非 0 視為 permanent（輸入檔本身的問題，重試不會成功）。
func (t *Tool) runFFmpeg(ctx context.Context, timeout time.Duration, args []string) (toolexec.Result, error) {
	res, err := t.run(ctx, toolexec.FFmpeg, timeout, args)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, &errclass.Error{
			Class: errclass.ClassPermanent, Source: "ffmpeg", ExitCode: res.ExitCode, Detail: lastLine(res.Stderr),
			Err: errors.New("ffmpeg 處理失敗"),
		}
	}
	return res, nil
}

// requireAbs 確認路徑都是絕對路徑：ffmpeg 與 ffprobe 的路徑參數不經過 "--"，相對或以 "-" 開頭的值會被當成選項。
func requireAbs(paths ...string) error {
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("media: 路徑必須是絕對路徑：%q", p)
		}
	}
	return nil
}

// secs 把秒數轉成 ffmpeg 的參數（小數點後三位）。
func secs(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }

// ffmpegInputArgs 是讀取使用者檔案時的共同前綴。
func ffmpegInputArgs() []string {
	return []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", formatWhitelist}
}

// probeJSON 是 ffprobe -of json 中用得到的部分。
type probeJSON struct {
	Streams []struct {
		Index       int               `json:"index"`
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		Tags        map[string]string `json:"tags"`
		Disposition map[string]int    `json:"disposition"`
		Width       int               `json:"width"`
		Height      int               `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration   string `json:"duration"`
		FormatName string `json:"format_name"`
	} `json:"format"`
}

// tag 以不分大小寫的 key 取出標籤（不同容器的大小寫不一致，例如 mkv 的 TITLE）。
func tag(tags map[string]string, key string) string {
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Probe 以 ffprobe 讀出容器的串流。ffprobe 失敗，或沒有音訊與影像串流時，錯誤包裝 ErrNotMedia（permanent）；
// 找不到 ffprobe 為 needs_action。
func (t *Tool) Probe(ctx context.Context, path string) (*Info, error) {
	if err := requireAbs(path); err != nil {
		return nil, err
	}
	args := []string{"-v", "error", "-hide_banner", "-protocol_whitelist", "file", "-format_whitelist", formatWhitelist,
		"-show_format", "-show_streams", "-of", "json", path}
	res, err := t.run(ctx, toolexec.FFprobe, probeTimeout, args)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, notMedia(lastLine(res.Stderr), res.ExitCode)
	}
	var pj probeJSON
	if err := json.Unmarshal(res.Stdout, &pj); err != nil {
		return nil, &errclass.Error{Class: errclass.ClassRetryable, Source: "ffprobe", Detail: "無法解析輸出", Err: err}
	}
	info := &Info{Format: pj.Format.FormatName}
	if d, err := strconv.ParseFloat(pj.Format.Duration, 64); err == nil && d > 0 {
		info.DurationS = d
	}
	for _, s := range pj.Streams {
		info.Streams = append(info.Streams, Stream{
			Index:       s.Index,
			Type:        s.CodecType,
			Codec:       s.CodecName,
			Lang:        CanonicalLang(tag(s.Tags, "language")),
			Title:       tag(s.Tags, "title"),
			Default:     s.Disposition["default"] == 1,
			Forced:      s.Disposition["forced"] == 1,
			AttachedPic: s.Disposition["attached_pic"] == 1,
			Width:       max(s.Width, 0),
			Height:      max(s.Height, 0),
		})
	}
	if !info.HasAV() {
		return nil, notMedia("沒有音訊或影像串流", 0)
	}
	return info, nil
}

func notMedia(detail string, exit int) error {
	return &errclass.Error{Class: errclass.ClassPermanent, Source: "ffprobe", ExitCode: exit, Detail: detail, Message: msgNotMedia, Err: ErrNotMedia}
}

// ExtractSubtitle 把容器中第 idx 條（絕對串流 index）字幕轉成 SRT 寫到 out。
func (t *Tool) ExtractSubtitle(ctx context.Context, in string, idx int, out string) error {
	if err := requireAbs(in, out); err != nil {
		return err
	}
	args := append(ffmpegInputArgs(), "-i", in, "-map", "0:"+strconv.Itoa(idx), "-c:s", "srt", "-f", "srt", "-y", out)
	_, err := t.runFFmpeg(ctx, subtitleTimeout, args)
	return err
}

// asrEncodeArgs 是語音轉錄前處理的編碼參數：單聲道、16 kHz、opus 32 kbps。
func asrEncodeArgs(out string) []string {
	return []string{"-vn", "-sn", "-dn", "-map", "0:a:0", "-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k", "-application", "voip", "-f", "ogg", "-y", out}
}

// PrepareASR 把 in 的第一條音訊壓縮成適合語音轉錄的 ogg/opus 寫到 out。
func (t *Tool) PrepareASR(ctx context.Context, in, out string) error {
	if err := requireAbs(in, out); err != nil {
		return err
	}
	args := append(append(ffmpegInputArgs(), "-i", in), asrEncodeArgs(out)...)
	_, err := t.runFFmpeg(ctx, encodeTimeout, args)
	return err
}

// ExtractASRChunk 從原始檔 in 取出 [startS, startS+lenS] 的音訊，同樣壓縮後寫到 out（從原始檔重新編碼）。
func (t *Tool) ExtractASRChunk(ctx context.Context, in, out string, startS, lenS float64) error {
	if err := requireAbs(in, out); err != nil {
		return err
	}
	args := append(ffmpegInputArgs(), "-ss", secs(startS), "-t", secs(lenS), "-i", in)
	args = append(args, asrEncodeArgs(out)...)
	_, err := t.runFFmpeg(ctx, encodeTimeout, args)
	return err
}

var (
	silenceStartRe = regexp.MustCompile(`silence_start:\s*(-?\d+(?:\.\d+)?)`)
	silenceEndRe   = regexp.MustCompile(`silence_end:\s*(-?\d+(?:\.\d+)?)`)
)

// Silences 偵測 prepared 在 [fromS, fromS+lenS] 視窗內的靜音，回傳絕對時間（已加上 fromS）。
// 只有開頭沒有結尾的尾段，以視窗終點補上。
func (t *Tool) Silences(ctx context.Context, in string, fromS, lenS float64) ([]Silence, error) {
	if err := requireAbs(in); err != nil {
		return nil, err
	}
	args := []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-protocol_whitelist", "file", "-format_whitelist", formatWhitelist,
		"-ss", secs(fromS), "-t", secs(lenS), "-i", in,
		"-af", "silencedetect=noise=-35dB:d=0.3", "-f", "null", "-"}
	res, err := t.runFFmpeg(ctx, silenceTimeout, args)
	if err != nil {
		return nil, err
	}
	return parseSilences(string(res.Stderr), fromS, lenS), nil
}

// parseSilences 解析 silencedetect 的 stderr；時間相對視窗起點，回傳時加上 fromS。
func parseSilences(stderr string, fromS, lenS float64) []Silence {
	var out []Silence
	var start float64
	open := false
	for _, line := range strings.Split(stderr, "\n") {
		if m := silenceStartRe.FindStringSubmatch(line); m != nil {
			if open { // 前一段沒有結尾：以這一段的開頭收尾
				out = append(out, Silence{StartS: fromS + start, EndS: fromS + max(start, parseF(m[1]))})
			}
			// 視窗開頭的靜音，start 可能是極小的負值。
			start, open = max(parseF(m[1]), 0), true
			continue
		}
		if m := silenceEndRe.FindStringSubmatch(line); m != nil {
			s := 0.0
			if open {
				s = start
			}
			out = append(out, Silence{StartS: fromS + s, EndS: fromS + max(s, parseF(m[1]))})
			open = false
		}
	}
	if open {
		out = append(out, Silence{StartS: fromS + start, EndS: fromS + max(start, lenS)})
	}
	return out
}

func parseF(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// Versions 回傳 ffmpeg 與 ffprobe 的版本（寫進快取鍵與來源紀錄）；取不到的工具不在結果中。
func (t *Tool) Versions(ctx context.Context) map[string]string {
	out := map[string]string{}
	for _, spec := range []toolexec.Spec{toolexec.FFmpeg, toolexec.FFprobe} {
		res, err := t.run(ctx, spec, versionTimeout, []string{"-version"})
		if err != nil || res.ExitCode != 0 {
			continue
		}
		if v := versionOf(firstLine(res.Stdout)); v != "" {
			out[spec.Name] = v
		}
	}
	return out
}

// versionOf 從 "ffmpeg version 6.1.1 Copyright …" 取出版本字串；格式不符時回傳整行。
func versionOf(line string) string {
	f := strings.Fields(line)
	for i, w := range f {
		if w == "version" && i+1 < len(f) {
			return f[i+1]
		}
	}
	return line
}

func firstLine(b []byte) string {
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// lastLine 回傳最後一個非空行（ffmpeg 與 ffprobe 的錯誤原因通常在最後）。
func lastLine(b []byte) string {
	lines := strings.Split(string(b), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// 樣式預覽（OUT-R9）的檔名與畫布。檔名固定，ffmpeg 的工作目錄是預覽專用的暫存目錄，
// 所以濾鏡參數不含任何需要跳脫的路徑。
const (
	PreviewASSName = "s.ass"
	PreviewPNGName = "out.png"
	// previewCanvas 是沒有影片可用時的合成畫布：深灰色、1280x720、1 幀。
	previewCanvas = "color=c=0x202020:s=1280x720:r=1:d=1"
)

// previewOutArgs 是預覽的輸出參數：用 libass 把 s.ass 疊在畫面上，輸出一張 PNG。
func previewOutArgs() []string {
	return []string{"-vf", "ass=" + PreviewASSName, "-frames:v", "1", "-an", "-sn", "-dn", "-update", "1", "-c:v", "png", "-y", PreviewPNGName}
}

// RenderSubtitleFrame 用 ffmpeg + libass 把 dir/s.ass 渲染到一個畫面，寫成 dir/out.png（OUT-R9）。
// in 是要抽幀的影片（絕對路徑），atS 是抽幀的時間（秒）；in 為空字串時改用合成的深灰色畫布。
// dir 必須是呼叫端建立的專用目錄（絕對路徑）：ffmpeg 以它為工作目錄，s.ass 與 out.png 都用相對路徑。
func (t *Tool) RenderSubtitleFrame(ctx context.Context, dir, in string, atS float64) error {
	if err := requireAbs(dir); err != nil {
		return err
	}
	var args []string
	if in == "" {
		args = append([]string{"-nostdin", "-hide_banner", "-v", "error", "-f", "lavfi", "-i", previewCanvas}, previewOutArgs()...)
	} else {
		if err := requireAbs(in); err != nil {
			return err
		}
		args = append(append(ffmpegInputArgs(), "-ss", secs(atS), "-i", in), previewOutArgs()...)
	}
	res, err := t.runIn(ctx, toolexec.FFmpeg, previewTimeout, dir, args)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return &errclass.Error{
			Class: errclass.ClassPermanent, Source: "ffmpeg", ExitCode: res.ExitCode, Detail: lastLine(res.Stderr),
			Err: errors.New("ffmpeg 渲染預覽失敗"),
		}
	}
	return nil
}

// 關鍵畫面（FRM）的逾時：場景偵測要讀完整部影片。
const (
	sceneTimeout = 30 * time.Minute
	frameTimeout = 2 * time.Minute
)

// DetectScenes 執行 ffmpeg 的場景偵測（FRM-R2），回傳 stderr（showinfo 與 lavfi.scene_score，由 frames.ParseShowinfo 解析）。
// threshold 只會是 0～1 的數字（處理方案的驗證），在這裡再夾一次，避免任意文字進入濾鏡字串。
func (t *Tool) DetectScenes(ctx context.Context, in string, threshold float64) (string, error) {
	if err := requireAbs(in); err != nil {
		return "", err
	}
	threshold = min(max(threshold, 0), 1)
	filter := "select='gt(scene," + strconv.FormatFloat(threshold, 'f', 4, 64) + ")',showinfo,metadata=print"
	args := []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-protocol_whitelist", "file", "-format_whitelist", formatWhitelist,
		"-i", in, "-an", "-sn", "-dn", "-vf", filter, "-f", "null", "-"}
	res, err := t.runFFmpeg(ctx, sceneTimeout, args)
	if err != nil {
		return "", err
	}
	return string(res.Stderr), nil
}

// ExtractFrame 擷取 in 在 atS 秒的一個畫面，縮放成 w×h 的 JPEG 寫到 out（固定 -q:v 2，FRM-R8）。
func (t *Tool) ExtractFrame(ctx context.Context, in string, atS float64, w, h int, out string) error {
	if err := requireAbs(in, out); err != nil {
		return err
	}
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return fmt.Errorf("media: 擷取尺寸 %dx%d 不正確", w, h)
	}
	args := append(ffmpegInputArgs(), "-ss", secs(atS), "-i", in,
		"-an", "-sn", "-dn", "-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:%d", w, h), "-q:v", "2", "-f", "mjpeg", "-y", out)
	_, err := t.runFFmpeg(ctx, frameTimeout, args)
	return err
}
