package source

import "testing"

func TestRecognize(t *testing.T) {
	tests := []struct {
		raw        string
		platform   string
		kind       Kind
		id         string
		part       int
		canonical  string
		recognized bool
	}{
		{"https://youtu.be/abc123", PlatformYouTube, KindVideo, "abc123", 0, "https://www.youtube.com/watch?v=abc123", true},
		{"https://www.youtube.com/shorts/abc123", PlatformYouTube, KindVideo, "abc123", 0, "https://www.youtube.com/watch?v=abc123", true},
		{"https://www.youtube.com/playlist?list=PL123", PlatformYouTube, KindPlaylist, "PL123", 0, "", true},
		{"https://www.youtube.com/@somechannel", PlatformYouTube, KindChannel, "@somechannel", 0, "", true},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=2", PlatformBilibili, KindVideo, "BV1xx411c7mD", 2, "https://www.bilibili.com/video/BV1xx411c7mD?p=2", true},
		{"https://www.bilibili.com/video/BV1xx411c7mD", PlatformBilibili, KindVideo, "BV1xx411c7mD", 1, "https://www.bilibili.com/video/BV1xx411c7mD?p=1", true},
		{"https://space.bilibili.com/12345", PlatformBilibili, KindChannel, "12345", 0, "", true},
		{"https://example.com/some/page", "", "", "", 0, "", false},
		{"ftp://www.youtube.com/watch?v=abc123", "", "", "", 0, "", false},
		{"not a url", "", "", "", 0, "", false},
		{"https://www.bilibili.com/video/notabvid", "", "", "", 0, "", false},
		{"https://space.bilibili.com/abc", "", "", "", 0, "", false},
	}
	for _, tt := range tests {
		got, ok := Recognize(tt.raw)
		if ok != tt.recognized {
			t.Errorf("Recognize(%q) ok = %v", tt.raw, ok)
			continue
		}
		if !ok {
			continue
		}
		if got.Platform != tt.platform || got.Kind != tt.kind || got.ExternalID != tt.id || got.Part != tt.part {
			t.Errorf("Recognize(%q) = %+v", tt.raw, got)
		}
		if tt.canonical != "" && got.URL != tt.canonical {
			t.Errorf("Recognize(%q).URL = %q，預期 %q", tt.raw, got.URL, tt.canonical)
		}
	}
}

func TestRef(t *testing.T) {
	tests := []struct {
		platform, id string
		part         int
		want         string
	}{
		{PlatformYouTube, "abc123", 0, "yt:abc123"},
		{PlatformBilibili, "BV1xx411c7mD", 3, "bili:BV1xx411c7mD/p3"},
		{PlatformPodcast, "show/ep-42", 0, "pod:show/ep-42"},
		{PlatformLocal, "talk.mp4", 0, "local:talk.mp4"},
	}
	for _, tt := range tests {
		if got := Ref(tt.platform, tt.id, tt.part); got != tt.want {
			t.Errorf("Ref(%s, %s, %d) = %q，預期 %q", tt.platform, tt.id, tt.part, got, tt.want)
		}
	}
}
