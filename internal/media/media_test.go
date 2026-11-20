package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// fakeRunner 記錄呼叫並回傳預先設定的結果。
type fakeRunner struct {
	calls []toolexec.Cmd
	res   toolexec.Result
	err   error
}

func (f *fakeRunner) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	f.calls = append(f.calls, c)
	return f.res, f.err
}

// newTool 建立 Tool，ffmpeg 與 ffprobe 指向暫存目錄中的可執行檔。
func newTool(t *testing.T, r toolexec.Runner) *Tool {
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
	return New(r, toolexec.Resolver{Config: cfg, Vars: map[string]string{}})
}

func binOf(t *Tool, spec toolexec.Spec) string { p, _ := t.Tools.Resolve(spec); return p }

const fw = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,mp3,ogg,wav,flac,aac,avi,mpegts,asf,srt,webvtt,ass"

func TestArgvGolden(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		call func(*Tool) error
		bin  toolexec.Spec
		want []string
	}{
		{
			"Probe",
			func(tl *Tool) error { _, err := tl.Probe(ctx, "/d/in.mp4"); return err },
			toolexec.FFprobe,
			[]string{"-v", "error", "-hide_banner", "-protocol_whitelist", "file", "-format_whitelist", fw,
				"-show_format", "-show_streams", "-of", "json", "/d/in.mp4"},
		},
		{
			"ExtractSubtitle",
			func(tl *Tool) error { return tl.ExtractSubtitle(ctx, "/d/in.mkv", 3, "/t/sub.srt") },
			toolexec.FFmpeg,
			[]string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", fw,
				"-i", "/d/in.mkv", "-map", "0:3", "-c:s", "srt", "-f", "srt", "-y", "/t/sub.srt"},
		},
		{
			"PrepareASR",
			func(tl *Tool) error { return tl.PrepareASR(ctx, "/d/in.mp4", "/t/a.ogg") },
			toolexec.FFmpeg,
			[]string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", fw,
				"-i", "/d/in.mp4", "-vn", "-sn", "-dn", "-map", "0:a:0", "-ac", "1", "-ar", "16000",
				"-c:a", "libopus", "-b:a", "32k", "-application", "voip", "-f", "ogg", "-y", "/t/a.ogg"},
		},
		{
			"ExtractASRChunk",
			func(tl *Tool) error { return tl.ExtractASRChunk(ctx, "/d/in.mp4", "/t/c0.ogg", 1497, 1507.5) },
			toolexec.FFmpeg,
			[]string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", fw,
				"-ss", "1497.000", "-t", "1507.500", "-i", "/d/in.mp4",
				"-vn", "-sn", "-dn", "-map", "0:a:0", "-ac", "1", "-ar", "16000",
				"-c:a", "libopus", "-b:a", "32k", "-application", "voip", "-f", "ogg", "-y", "/t/c0.ogg"},
		},
		{
			"Silences",
			func(tl *Tool) error { _, err := tl.Silences(ctx, "/t/a.ogg", 1472, 60); return err },
			toolexec.FFmpeg,
			[]string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-protocol_whitelist", "file",
				"-ss", "1472.000", "-t", "60.000", "-i", "/t/a.ogg",
				"-af", "silencedetect=noise=-35dB:d=0.3", "-f", "null", "-"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRunner{res: toolexec.Result{Stdout: mustRead(t, "video_mp4.json")}}
			tl := newTool(t, fr)
			if err := tt.call(tl); err != nil {
				t.Fatal(err)
			}
			if len(fr.calls) != 1 {
				t.Fatalf("呼叫 %d 次", len(fr.calls))
			}
			c := fr.calls[0]
			if c.Path != binOf(tl, tt.bin) {
				t.Errorf("Path = %q", c.Path)
			}
			if !reflect.DeepEqual(c.Args, tt.want) {
				t.Errorf("argv =\n%q\n要\n%q", c.Args, tt.want)
			}
			if c.Timeout <= 0 {
				t.Error("應設定逾時")
			}
		})
	}
}

func TestVersionsArgv(t *testing.T) {
	fr := &fakeRunner{res: toolexec.Result{Stdout: []byte("ffmpeg version 6.1.1-3ubuntu5 Copyright (c) 2000-2023 the FFmpeg developers\nbuilt with gcc\n")}}
	tl := newTool(t, fr)
	got := tl.Versions(context.Background())
	want := map[string]string{"ffmpeg": "6.1.1-3ubuntu5", "ffprobe": "6.1.1-3ubuntu5"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Versions = %v", got)
	}
	for i, c := range fr.calls {
		if !reflect.DeepEqual(c.Args, []string{"-version"}) {
			t.Errorf("call %d argv = %q", i, c.Args)
		}
	}
	if len(fr.calls) != 2 || fr.calls[0].Path != binOf(tl, toolexec.FFmpeg) || fr.calls[1].Path != binOf(tl, toolexec.FFprobe) {
		t.Fatalf("calls = %+v", fr.calls)
	}

	// 取不到版本的工具不在結果中。
	fr = &fakeRunner{res: toolexec.Result{ExitCode: 1}}
	if got := newTool(t, fr).Versions(context.Background()); len(got) != 0 {
		t.Fatalf("Versions = %v", got)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func probe(t *testing.T, fixture string) (*Info, error) {
	t.Helper()
	fr := &fakeRunner{res: toolexec.Result{Stdout: mustRead(t, fixture)}}
	return newTool(t, fr).Probe(context.Background(), "/d/x")
}

func TestProbeVideo(t *testing.T) {
	info, err := probe(t, "video_mp4.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.DurationS != 600.021333 || info.Format != "mov,mp4,m4a,3gp,3g2,mj2" {
		t.Fatalf("info = %+v", info)
	}
	want := []Stream{
		{Index: 0, Type: "video", Codec: "h264", Default: true},
		{Index: 1, Type: "audio", Codec: "aac", Lang: "en", Default: true},
		{Index: 2, Type: "subtitle", Codec: "mov_text", Lang: "en", Default: true},
		{Index: 3, Type: "subtitle", Codec: "mov_text", Lang: "ja", Title: "日本語 (forced)", Forced: true},
		{Index: 4, Type: "data", Codec: "bin_data", Default: true},
	}
	if !reflect.DeepEqual(info.Streams, want) {
		t.Fatalf("streams =\n%+v\n要\n%+v", info.Streams, want)
	}
	if !info.HasAV() || info.Kind() != "video" {
		t.Fatalf("HasAV=%v Kind=%s", info.HasAV(), info.Kind())
	}
}

func TestProbeAudioWithCover(t *testing.T) {
	info, err := probe(t, "audio_cover.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind() != "audio" || !info.HasAV() {
		t.Fatalf("Kind=%s HasAV=%v", info.Kind(), info.HasAV())
	}
	if !info.Streams[1].AttachedPic || info.Streams[1].Type != "video" {
		t.Fatalf("封面圖：%+v", info.Streams[1])
	}
	if info.Streams[0].Title != "Long Talk" { // TITLE 大寫的標籤
		t.Fatalf("title = %q", info.Streams[0].Title)
	}
	if info.Streams[0].Lang != "" {
		t.Fatalf("沒有語言標記：%q", info.Streams[0].Lang)
	}
}

func TestProbeNotMedia(t *testing.T) {
	check := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, ErrNotMedia) {
			t.Fatalf("err = %v，要 ErrNotMedia", err)
		}
		if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassPermanent {
			t.Fatalf("class = %v %v", c, ok)
		}
		var e *errclass.Error
		if !errors.As(err, &e) || e.Message == "" {
			t.Fatalf("要有給使用者的中文訊息：%v", err)
		}
	}
	t.Run("只有封面圖與字幕", func(t *testing.T) {
		_, err := probe(t, "cover_only.json")
		check(t, err)
	})
	t.Run("沒有串流", func(t *testing.T) {
		fr := &fakeRunner{res: toolexec.Result{Stdout: []byte(`{"streams":[],"format":{"format_name":"srt"}}`)}}
		_, err := newTool(t, fr).Probe(context.Background(), "/d/x")
		check(t, err)
	})
	t.Run("ffprobe 非 0 結束", func(t *testing.T) {
		fr := &fakeRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte("[mov,mp4,m4a,3gp,3g2,mj2 @ 0x1] moov atom not found\n/d/x: Invalid data found when processing input\n")}}
		_, err := newTool(t, fr).Probe(context.Background(), "/d/x")
		check(t, err)
		var e *errclass.Error
		_ = errors.As(err, &e)
		if !strings.Contains(e.Detail, "Invalid data found") || e.ExitCode != 1 {
			t.Fatalf("detail = %q exit = %d", e.Detail, e.ExitCode)
		}
	})
}

func TestProbeErrors(t *testing.T) {
	ctx := context.Background()
	t.Run("找不到工具", func(t *testing.T) {
		tl := New(&fakeRunner{}, toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}})
		_, err := tl.Probe(ctx, "/d/x")
		if c, _ := errclass.ClassOf(err); c != errclass.ClassNeedsAction {
			t.Fatalf("err = %v", err)
		}
		if errors.Is(err, ErrNotMedia) {
			t.Fatal("找不到工具不是 ErrNotMedia")
		}
		if err := tl.PrepareASR(ctx, "/d/x", "/t/y"); err == nil {
			t.Fatal("PrepareASR 也應失敗")
		} else if c, _ := errclass.ClassOf(err); c != errclass.ClassNeedsAction {
			t.Fatalf("PrepareASR err = %v", err)
		}
	})
	t.Run("相對路徑", func(t *testing.T) {
		fr := &fakeRunner{}
		tl := newTool(t, fr)
		if _, err := tl.Probe(ctx, "-evil"); err == nil {
			t.Fatal("相對路徑應拒絕")
		}
		if err := tl.ExtractSubtitle(ctx, "/d/x", 1, "out.srt"); err == nil {
			t.Fatal("相對輸出路徑應拒絕")
		}
		if len(fr.calls) != 0 {
			t.Fatal("不應執行工具")
		}
	})
	t.Run("執行失敗為 retryable", func(t *testing.T) {
		tl := newTool(t, &fakeRunner{err: toolexec.ErrOutputTooLarge})
		_, err := tl.Probe(ctx, "/d/x")
		if c, _ := errclass.ClassOf(err); c != errclass.ClassRetryable {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("取消不包裝", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		tl := newTool(t, &fakeRunner{err: errors.New("killed")})
		if _, err := tl.Probe(cctx, "/d/x"); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ffmpeg 非 0 結束為 permanent", func(t *testing.T) {
		tl := newTool(t, &fakeRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte("Output file does not contain any stream\n")}})
		err := tl.PrepareASR(ctx, "/d/x", "/t/y")
		if c, _ := errclass.ClassOf(err); c != errclass.ClassPermanent {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestParseSilences(t *testing.T) {
	// 真實格式的 silencedetect 輸出（含無關行）。
	stderr := `Input #0, ogg, from '/t/a.ogg':
  Duration: 00:50:04.12, start: 0.000000, bitrate: 32 kb/s
Stream mapping:
  Stream #0:0 -> #0:0 (opus (libopus) -> pcm_s16le (native))
[silencedetect @ 0x55d0c2b3a440] silence_start: 0.0123
[silencedetect @ 0x55d0c2b3a440] silence_end: 0.8 | silence_duration: 0.7877
[silencedetect @ 0x55d0c2b3a440] silence_start: 29.75
[silencedetect @ 0x55d0c2b3a440] silence_end: 30.2 | silence_duration: 0.45
[silencedetect @ 0x55d0c2b3a440] silence_start: 58.4
size=N/A time=00:01:00.00 bitrate=N/A speed= 300x
`
	got := parseSilences(stderr, 1472, 60)
	want := []Silence{
		{StartS: 1472.0123, EndS: 1472.8},
		{StartS: 1501.75, EndS: 1502.2},
		{StartS: 1530.4, EndS: 1532}, // 尾段以視窗終點補上
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if abs(got[i].StartS-want[i].StartS) > 1e-9 || abs(got[i].EndS-want[i].EndS) > 1e-9 {
			t.Errorf("[%d] = %+v，要 %+v", i, got[i], want[i])
		}
	}

	if s := parseSilences("no silence here\n", 10, 5); len(s) != 0 {
		t.Fatalf("沒有靜音：%+v", s)
	}
	// 視窗開頭的負值 start 會被夾到 0。
	s := parseSilences("silence_start: -0.00133\nsilence_end: 0.5 | silence_duration: 0.5\n", 100, 60)
	if len(s) != 1 || s[0].StartS != 100 || s[0].EndS != 100.5 {
		t.Fatalf("負值 start：%+v", s)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestSilencesViaRunner(t *testing.T) {
	fr := &fakeRunner{res: toolexec.Result{Stderr: []byte(
		"[silencedetect @ 0x1] silence_start: 29.9\n[silencedetect @ 0x1] silence_end: 30.3 | silence_duration: 0.4\n")}}
	got, err := newTool(t, fr).Silences(context.Background(), "/t/a.ogg", 1472, 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || abs(got[0].StartS-1501.9) > 1e-9 || abs(got[0].EndS-1502.3) > 1e-9 {
		t.Fatalf("got %+v", got)
	}
}

func TestBCP47(t *testing.T) {
	tests := map[string]string{
		"eng": "en", "jpn": "ja", "zho": "zh", "chi": "zh", "kor": "ko",
		"fra": "fr", "fre": "fr", "deu": "de", "ger": "de", "spa": "es",
		"ita": "it", "por": "pt", "rus": "ru",
		"ENG": "en", " jpn ": "ja",
		"en": "en", "ja": "ja", "zh": "zh",
		"und": "", "": "", "xxx": "", "mul": "", "zxx": "", "english": "",
	}
	for in, want := range tests {
		if got := BCP47(in); got != want {
			t.Errorf("BCP47(%q) = %q，要 %q", in, got, want)
		}
	}
}

func TestIsTextSubtitle(t *testing.T) {
	for _, c := range []string{"subrip", "ass", "ssa", "mov_text", "webvtt", "text", "SubRip"} {
		if !IsTextSubtitle(c) {
			t.Errorf("%s 應是文字字幕", c)
		}
	}
	for _, c := range []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "", "mjpeg"} {
		if IsTextSubtitle(c) {
			t.Errorf("%s 不是文字字幕", c)
		}
	}
}

func TestInfoKind(t *testing.T) {
	cover := &Info{Streams: []Stream{{Type: "video", AttachedPic: true}}}
	if cover.HasAV() {
		t.Fatal("只有封面圖不算影音")
	}
	if (&Info{Streams: []Stream{{Type: "video"}}}).Kind() != "video" {
		t.Fatal("有影像串流是 video")
	}
	if (&Info{Streams: []Stream{{Type: "audio"}, {Type: "video", AttachedPic: true}}}).Kind() != "audio" {
		t.Fatal("封面圖不算影片")
	}
}
