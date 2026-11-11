package youtube

import (
	"net/url"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		raw  string
		want Target
		ok   bool
	}{
		{"https://www.youtube.com/watch?v=abc123", Target{Video, "abc123"}, true},
		{"https://youtube.com/watch?v=abc123&t=30", Target{Video, "abc123"}, true},
		{"https://m.youtube.com/watch?v=abc123", Target{Video, "abc123"}, true},
		{"https://youtu.be/abc123", Target{Video, "abc123"}, true},
		{"https://youtu.be/abc123?si=xyz", Target{Video, "abc123"}, true},
		{"https://www.youtube.com/shorts/abc123", Target{Video, "abc123"}, true},
		{"https://www.youtube.com/live/abc123", Target{Video, "abc123"}, true},
		// 同時有影片與播放清單時，視為影片（yt-dlp 以 --no-playlist 執行）。
		{"https://www.youtube.com/watch?v=abc123&list=PL123", Target{Video, "abc123"}, true},
		{"https://www.youtube.com/playlist?list=PL123", Target{Playlist, "PL123"}, true},
		{"https://www.youtube.com/@somechannel", Target{Channel, "@somechannel"}, true},
		{"https://www.youtube.com/@somechannel/videos", Target{Channel, "@somechannel"}, true},
		{"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv", Target{Channel, "UCabcdefghijklmnopqrstuv"}, true},
		{"https://www.youtube.com/c/SomeName", Target{Channel, "SomeName"}, true},
		// 不辨識。
		{"https://www.youtube.com/watch", Target{}, false},
		{"https://www.youtube.com/watch?v=", Target{}, false},
		{"https://www.youtube.com/watch?v=a%20b", Target{}, false},
		{"https://www.youtube.com/channel/short", Target{}, false},
		{"https://www.youtube.com/", Target{}, false},
		{"https://notyoutube.com/watch?v=abc123", Target{}, false},
		{"https://youtube.com.evil.test/watch?v=abc123", Target{}, false},
		{"https://example.com/some/page", Target{}, false},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.raw)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := Parse(u)
		if ok != tt.ok || got != tt.want {
			t.Errorf("Parse(%q) = %+v, %v；預期 %+v, %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

func TestWatchURLEscapes(t *testing.T) {
	if got := WatchURL("a-b_c"); got != "https://www.youtube.com/watch?v=a-b_c" {
		t.Errorf("WatchURL = %q", got)
	}
}
