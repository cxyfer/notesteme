package bilibili

import (
	"io"
	"log/slog"
	"testing"
)

// yt-dlp 的 JSON 輸出不可信，解析不得 panic。
func FuzzParseInfo(f *testing.F) {
	f.Add([]byte(`{"id":"BV1xx411c7mD","title":"t","duration":10,"subtitles":{"zh-Hans":[{"ext":"srt","url":"u"}]}}`))
	f.Add([]byte(`{"id":"BV1","subtitles":{"ai-zh":null},"timestamp":1}`))
	f.Add([]byte(`null`))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseInfo(b, log)
	})
}
