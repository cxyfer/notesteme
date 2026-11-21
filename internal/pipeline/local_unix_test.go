//go:build unix

package pipeline

import (
	"context"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// 外掛字幕是 FIFO（不是一般檔案）：不能卡住，記為 rejected／unreadable 並退回內嵌字幕。
func TestLocalSidecarFIFOFallsBackToEmbedded(t *testing.T) {
	l, g := newLocalGroqRig(t)
	l.fm.streams = videoStreams + `,{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}`
	l.fm.subtitles = map[int]string{2: srtEN}
	media := l.file("movie.mkv", "MKV")
	if err := syscall.Mkfifo(filepath.Join(l.root, "movie.en.srt"), 0o644); err != nil {
		t.Skip("無法建立 FIFO：", err)
	}

	done := make(chan struct{})
	var res result
	var err error
	go func() {
		defer close(done)
		res, err = l.p.runTranscript(context.Background(), l.item(media), groqLocalLang())
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("讀取 FIFO 卡住了")
	}
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"local-sidecar:rejected:unreadable", "local-embedded:used:"}
	if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 0 {
		t.Errorf("probes = %v，Groq %d 次，預期 %v", got, g.calls(), want)
	}
}
