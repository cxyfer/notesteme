package youtube

import (
	"net/url"
	"testing"
)

// yt-dlp 的 JSON 輸出與使用者輸入的 URL 都不可信，解析不得 panic。
func FuzzParseInfo(f *testing.F) {
	f.Add([]byte(`{"id":"abc","title":"t","duration":12.5,"timestamp":1700000000,"subtitles":{"en":[{"ext":"vtt","url":"u"}]}}`))
	f.Add([]byte(`{"id":"abc","upload_date":"20240102","duration":-1,"automatic_captions":null}`))
	f.Add([]byte(`{"id":"abc","subtitles":{"en":null},"language":null}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseInfo(b)
	})
}

func FuzzParseURL(f *testing.F) {
	for _, s := range []string{"https://www.youtube.com/watch?v=abc123xxxxx", "https://youtu.be/abc123xxxxx?t=1", "https://youtube.com/playlist?list=PL1", "https://youtube.com/shorts/x", "http://a/%zz"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if u, err := url.Parse(s); err == nil {
			_, _ = Parse(u)
		}
	})
}
