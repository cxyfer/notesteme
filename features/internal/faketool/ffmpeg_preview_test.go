package faketool

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const previewASS = "[Script Info]\nTitle: t\n\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Translation,Noto Sans,52\n\n" +
	"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
	"Dialogue: 0,0:00:01.00,0:00:03.00,Translation,,0,0,0,,hello\n"

const previewCanvasArg = "color=c=0x202020:s=1280x720:r=1:d=1"

// previewOut 是預覽共用的輸出參數（和 internal/media 的 previewOutArgs 一致）。
func previewOut() []string {
	return []string{"-vf", "ass=s.ass", "-frames:v", "1", "-an", "-sn", "-dn", "-update", "1", "-c:v", "png", "-y", "out.png"}
}

func previewCanvasArgs() []string {
	return append([]string{"-nostdin", "-hide_banner", "-v", "error", "-f", "lavfi", "-i", previewCanvasArg}, previewOut()...)
}

func previewFileArgs(in, ss string) []string {
	return append([]string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov,mp4,matroska,webm",
		"-ss", ss, "-i", in}, previewOut()...)
}

func (s *scenario) writeASS(content string) {
	s.t.Helper()
	if err := os.WriteFile(filepath.Join(s.work, "s.ass"), []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func TestFFmpegPreviewCanvas(t *testing.T) {
	s := newScenario(t, State{})
	s.writeASS(previewASS)
	if code, _, errs := s.exec("ffmpeg", previewCanvasArgs()...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	b, err := os.ReadFile(filepath.Join(s.work, "out.png"))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil || img.Bounds().Dx() != 1280 || img.Bounds().Dy() != 720 {
		t.Fatalf("輸出不是 1280x720 的 PNG：%v", err)
	}
}

func TestFFmpegPreviewVideo(t *testing.T) {
	s := newScenario(t, State{})
	s.writeASS(previewASS)
	in := s.writeMarkerFile("movie.mp4", Media{Item: "local:movie.mp4", Duration: 60, Streams: []MediaStream{{Type: "video", Codec: "h264"}, {Type: "audio", Codec: "aac"}}})
	if code, _, errs := s.exec("ffmpeg", previewFileArgs(in, "2.5")...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(s.work, "out.png")); err != nil {
		t.Fatal(err)
	}
	// 超過長度：失敗（1），和真實的 ffmpeg 一樣沒有輸出。
	if code, _, errs := s.exec("ffmpeg", previewFileArgs(in, "61")...); code != 1 || !strings.Contains(errs, "empty") {
		t.Errorf("-ss 超過長度：code=%d stderr=%q", code, errs)
	}
	// 只有音訊：失敗（1）。
	audio := s.writeMarkerFile("a.mp3", Media{Item: "x", Duration: 60})
	if code, _, _ := s.exec("ffmpeg", previewFileArgs(audio, "1")...); code != 1 {
		t.Errorf("沒有影像串流：code=%d", code)
	}
	if code, _, _ := s.exec("ffmpeg", previewFileArgs(filepath.Join(s.work, "missing.mp4"), "1")...); code != 1 {
		t.Errorf("找不到輸入：code=%d", code)
	}
}

func TestFFmpegPreviewFailures(t *testing.T) {
	// 沒有 libass：找不到 ass 濾鏡，結束碼 1。
	s := newScenario(t, State{FFmpegNoLibass: true})
	s.writeASS(previewASS)
	if code, _, errs := s.exec("ffmpeg", previewCanvasArgs()...); code != 1 || !strings.Contains(errs, "No such filter: 'ass'") {
		t.Errorf("沒有 libass：code=%d stderr=%q", code, errs)
	}
	// 字幕檔缺少或內容不足以渲染：結束碼 1。
	s = newScenario(t, State{})
	if code, _, _ := s.exec("ffmpeg", previewCanvasArgs()...); code != 1 {
		t.Errorf("沒有 s.ass：code=%d", code)
	}
	for name, ass := range map[string]string{
		"沒有樣式":        "[Script Info]\nTitle: t\n\n[Events]\nDialogue: 0,0:00:01.00,0:00:03.00,X,,0,0,0,,hi\n",
		"沒有 Dialogue": strings.Split(previewASS, "Dialogue:")[0],
		"空檔":          "",
	} {
		s.writeASS(ass)
		if code, _, _ := s.exec("ffmpeg", previewCanvasArgs()...); code != 1 {
			t.Errorf("%s：code=%d", name, code)
		}
	}
}

func TestFFmpegPreviewArgvContract(t *testing.T) {
	s := newScenario(t, State{})
	s.writeASS(previewASS)
	in := s.writeMarkerFile("movie.mp4", Media{Item: "local:movie.mp4", Duration: 60, Streams: []MediaStream{{Type: "video", Codec: "h264"}}})
	swap := func(args []string, flag, value string) []string {
		c := append([]string{}, args...)
		for i := range c {
			if c[i] == flag && i+1 < len(c) {
				c[i+1] = value
			}
		}
		return c
	}
	drop := func(args []string, flag string, hasValue bool) []string {
		var c []string
		for i := 0; i < len(args); i++ {
			if args[i] == flag {
				if hasValue {
					i++
				}
				continue
			}
			c = append(c, args[i])
		}
		return c
	}
	canvas, file := previewCanvasArgs(), previewFileArgs(in, "1")
	cases := map[string][]string{
		"畫布：缺 -nostdin":             drop(canvas, "-nostdin", false),
		"畫布：缺 -an":                  drop(canvas, "-an", false),
		"畫布：缺 -y":                   drop(canvas, "-y", false),
		"畫布：-f 不是 lavfi":            swap(canvas, "-f", "image2"),
		"畫布：不是 color 來源":            swap(canvas, "-i", "testsrc=s=1280x720"),
		"畫布：帶了 -ss":                 append([]string{"-ss", "1"}, canvas...),
		"畫布：帶了 -protocol_whitelist": append([]string{"-protocol_whitelist", "file"}, canvas...),
		"濾鏡不是 ass=s.ass":            swap(canvas, "-vf", "ass=/etc/passwd"),
		"濾鏡帶了路徑":                    swap(canvas, "-vf", "ass=/tmp/x/s.ass"),
		"-frames:v 不是 1":            swap(canvas, "-frames:v", "10"),
		"輸出不是 out.png":              append(drop(canvas, "out.png", false), "shot.png"),
		"編碼器不是 png":                 swap(canvas, "-c:v", "mjpeg"),
		"-v 不是 error":               swap(canvas, "-v", "info"),
		"影片：缺 -protocol_whitelist":  drop(file, "-protocol_whitelist", true),
		"影片：protocol 不是 file":       swap(file, "-protocol_whitelist", "file,http"),
		"影片：缺 -format_whitelist":    drop(file, "-format_whitelist", true),
		"影片：format 含 hls":           swap(file, "-format_whitelist", "mp4,hls"),
		"影片：缺 -ss":                  drop(file, "-ss", true),
		"影片：-ss 是負數":                swap(file, "-ss", "-1"),
		"影片：-i 是相對路徑":               swap(file, "-i", "movie.mp4"),
		"影片：帶了 -t":                  append([]string{"-t", "5"}, file...),
		"未知旗標":                      append([]string{"-frobnicate"}, canvas...),
	}
	for name, args := range cases {
		if code, _, errs := s.exec("ffmpeg", args...); code != 2 {
			t.Errorf("%s：code=%d stderr=%q，預期 2", name, code, errs)
		}
	}
	// -ss 在 -i 之後：用法錯誤。
	late := append([]string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", "mov,mp4", "-i", in, "-ss", "1"}, previewOut()...)
	if code, _, _ := s.exec("ffmpeg", late...); code != 2 {
		t.Errorf("-ss 在 -i 之後：code=%d", code)
	}
	// 契約內的呼叫成功。
	if code, _, errs := s.exec("ffmpeg", canvas...); code != 0 {
		t.Errorf("契約：code=%d stderr=%q", code, errs)
	}
}
