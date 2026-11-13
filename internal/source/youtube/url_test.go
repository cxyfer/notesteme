package youtube

import (
	"net/url"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		raw  string
		want Target
		ok   bool
	}{
		{"https://www.youtube.com/watch?v=abc123xxxxx", Target{Video, "abc123xxxxx"}, true},
		{"https://youtube.com/watch?v=abc123xxxxx&t=30", Target{Video, "abc123xxxxx"}, true},
		{"https://m.youtube.com/watch?v=abc123xxxxx", Target{Video, "abc123xxxxx"}, true},
		{"https://youtu.be/abc123xxxxx", Target{Video, "abc123xxxxx"}, true},
		// 影片 ID 固定 11 個字元，可含 - 與 _。
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", Target{Video, "dQw4w9WgXcQ"}, true},
		{"https://www.youtube.com/watch?v=a-b_c-d_e-f", Target{Video, "a-b_c-d_e-f"}, true},
		{"https://youtu.be/dQw4w9WgXcQ", Target{Video, "dQw4w9WgXcQ"}, true},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", Target{Video, "dQw4w9WgXcQ"}, true},
		{"https://youtu.be/abc123xxxxx?si=xyz", Target{Video, "abc123xxxxx"}, true},
		{"https://www.youtube.com/shorts/abc123xxxxx", Target{Video, "abc123xxxxx"}, true},
		{"https://www.youtube.com/live/abc123xxxxx", Target{Video, "abc123xxxxx"}, true},
		// 同時有影片與播放清單時，視為影片（yt-dlp 以 --no-playlist 執行）。
		{"https://www.youtube.com/watch?v=abc123xxxxx&list=PL123", Target{Video, "abc123xxxxx"}, true},
		{"https://www.youtube.com/playlist?list=PL123", Target{Playlist, "PL123"}, true},
		{"https://www.youtube.com/@somechannel", Target{Channel, "@somechannel"}, true},
		{"https://www.youtube.com/@somechannel/videos", Target{Channel, "@somechannel"}, true},
		{"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv", Target{Channel, "UCabcdefghijklmnopqrstuv"}, true},
		{"https://www.youtube.com/c/SomeName", Target{Channel, "SomeName"}, true},
		// 不辨識：影片 ID 不是 11 個字元（10、12、過短、過長），或含非法字元。
		{"https://www.youtube.com/watch?v=abc123xxxx", Target{}, false},
		{"https://www.youtube.com/watch?v=abc123xxxxxx", Target{}, false},
		{"https://www.youtube.com/watch?v=v", Target{}, false},
		{"https://www.youtube.com/watch?v=v1", Target{}, false},
		{"https://www.youtube.com/watch?v=" + strings.Repeat("a", 64), Target{}, false},
		{"https://www.youtube.com/watch?v=abc123xxx.x", Target{}, false},
		{"https://www.youtube.com/watch?v=abc123xxx!x", Target{}, false},
		{"https://www.youtube.com/watch?v=abc123xx%20x", Target{}, false},
		{"https://www.youtube.com/watch?v=abc123xxxé", Target{}, false},
		{"https://youtu.be/abc123xxxx", Target{}, false},
		{"https://youtu.be/abc123xxxxxx", Target{}, false},
		{"https://youtu.be/abc123xx.xx", Target{}, false},
		{"https://www.youtube.com/shorts/abc123xxxx", Target{}, false},
		{"https://www.youtube.com/shorts/abc123xxxxxx", Target{}, false},
		{"https://www.youtube.com/shorts/abc123xx/xx", Target{}, false},
		{"https://www.youtube.com/live/abc123xxxx", Target{}, false},
		{"https://www.youtube.com/watch", Target{}, false},
		{"https://www.youtube.com/watch?v=", Target{}, false},
		{"https://www.youtube.com/watch?v=a%20b", Target{}, false},
		{"https://www.youtube.com/channel/short", Target{}, false},
		{"https://www.youtube.com/", Target{}, false},
		{"https://notyoutube.com/watch?v=abc123xxxxx", Target{}, false},
		{"https://youtube.com.evil.test/watch?v=abc123xxxxx", Target{}, false},
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
