package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// probesOf 把 source_probes 轉成「來源:狀態:原因」，方便比對。
func probesOf(res result) []string {
	var out []string
	for _, p := range res.probes {
		out = append(out, p.SourceID+":"+p.State+":"+p.Reason)
	}
	return out
}

func newLocalGroqRig(t *testing.T) (*localRig, *fakeGroq) {
	t.Helper()
	g := &fakeGroq{lang: "ja", baseURL: "u", model: "m"}
	l := newLocalRig(t, func(d *Deps) { d.Groq = g })
	return l, g
}

// groqLocalLang 是只用 groq 當 fallback 的語言設定。
func groqLocalLang() Language {
	lang := DefaultLanguage()
	lang.Fallback = []string{"groq"}
	return lang
}

// 選中的外掛字幕在允許的目錄之外（symlink）：記為 rejected／unreadable，沒有其他字幕時改用 ASR，步驟不失敗。
func TestLocalSidecarOutsideRootFallsBackToASR(t *testing.T) {
	l, g := newLocalGroqRig(t)
	media := l.file("movie.mp4", "MEDIA")
	outside := filepath.Join(t.TempDir(), "secret.srt")
	if err := os.WriteFile(outside, []byte(srtEN), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(l.root, "movie.en.srt")); err != nil {
		t.Skip("無法建立 symlink：", err)
	}

	res, err := l.p.runTranscript(context.Background(), l.item(media), groqLocalLang())
	if err != nil {
		t.Fatalf("外掛字幕有問題不該讓步驟失敗：%v", err)
	}
	want := []string{"local-sidecar:rejected:unreadable", "local-embedded:unavailable:no_track", "groq:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 1 {
		t.Errorf("probes = %v，Groq 呼叫 %d 次，預期 %v 與 1 次", got, g.calls(), want)
	}
}

// 外掛字幕內容無法解析：退回內嵌字幕。
func TestLocalSidecarUnparsableFallsBackToEmbedded(t *testing.T) {
	l, g := newLocalGroqRig(t)
	l.fm.streams = videoStreams + `,{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}`
	l.fm.subtitles = map[int]string{2: srtEN}
	media := l.file("movie.mkv", "MKV")
	l.file("movie.en.srt", "this is not a subtitle file\n")

	res, err := l.p.runTranscript(context.Background(), l.item(media), groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"local-sidecar:rejected:unreadable", "local-embedded:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 0 || res.artifact == nil || res.artifact.Lang != "en" {
		t.Errorf("probes = %v，Groq %d 次，artifact = %+v，預期 %v", got, g.calls(), res.artifact, want)
	}
}

// 第一條外掛字幕壞了：改用下一條外掛字幕，原文語言也依實際採用的那條推斷。
func TestLocalBrokenSidecarFallsBackToNextSidecar(t *testing.T) {
	l, _ := newLocalGroqRig(t)
	media := l.file("movie.mp4", "MEDIA")
	l.file("movie.en.srt", "garbage")
	l.file("movie.ja.srt", srtEN)

	res, err := l.p.runTranscript(context.Background(), l.item(media), groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"local-sidecar:rejected:unreadable", "local-sidecar:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) {
		t.Errorf("probes = %v，預期 %v", got, want)
	}
	if res.artifact == nil || res.artifact.Lang != "ja" || res.meta == nil || res.meta.item.OriginalLang != "ja" {
		t.Errorf("artifact = %+v，meta = %+v", res.artifact, res.meta)
	}
}

// 外掛字幕超過大小上限也是 rejected，不是整個步驟失敗。
func TestLocalOversizedSidecarIsRejected(t *testing.T) {
	l, g := newLocalGroqRig(t)
	media := l.file("movie.mp4", "MEDIA")
	big := l.file("movie.en.srt", "")
	if err := os.Truncate(big, maxSidecarBytes+1); err != nil {
		t.Fatal(err)
	}
	res, err := l.p.runTranscript(context.Background(), l.item(media), groqLocalLang())
	if err != nil {
		t.Fatal(err)
	}
	if got := probesOf(res); got[0] != "local-sidecar:rejected:unreadable" || g.calls() != 1 {
		t.Errorf("probes = %v，Groq %d 次", got, g.calls())
	}
}

// 內嵌字幕抽取失敗：記為 rejected，改用 ASR。
func TestLocalEmbeddedExtractionFailureFallsBackToASR(t *testing.T) {
	l, g := newLocalGroqRig(t)
	l.fm.streams = videoStreams + `,{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}`
	// l.fm.subtitles 沒有 index 2：假 ffmpeg 以 exit 1 結束，與真實的「找不到串流」相同。
	media := l.file("movie.mkv", "MKV")

	res, err := l.p.runTranscript(context.Background(), l.item(media), groqLocalLang())
	if err != nil {
		t.Fatalf("內嵌字幕抽取失敗不該讓步驟失敗：%v", err)
	}
	want := []string{"local-sidecar:unavailable:no_track", "local-embedded:rejected:unreadable", "groq:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 1 {
		t.Errorf("probes = %v，Groq %d 次，預期 %v 與 1 次", got, g.calls(), want)
	}
}
