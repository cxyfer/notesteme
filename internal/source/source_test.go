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
		{"https://youtu.be/abc123xxxxx", PlatformYouTube, KindVideo, "abc123xxxxx", 0, "https://www.youtube.com/watch?v=abc123xxxxx", true},
		{"https://www.youtube.com/shorts/abc123xxxxx", PlatformYouTube, KindVideo, "abc123xxxxx", 0, "https://www.youtube.com/watch?v=abc123xxxxx", true},
		{"https://www.youtube.com/playlist?list=PL123", PlatformYouTube, KindPlaylist, "PL123", 0, "", true},
		{"https://www.youtube.com/@somechannel", PlatformYouTube, KindChannel, "@somechannel", 0, "", true},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=2", PlatformBilibili, KindVideo, "BV1xx411c7mD", 2, "https://www.bilibili.com/video/BV1xx411c7mD?p=2", true},
		// 沒有 p：Part 為 0，代表「未指定」（ING-R3：預設展開全部分P）。
		{"https://www.bilibili.com/video/BV1xx411c7mD", PlatformBilibili, KindVideo, "BV1xx411c7mD", 0, "https://www.bilibili.com/video/BV1xx411c7mD", true},
		{"https://www.bilibili.com/video/BV1xx411c7mD?p=1", PlatformBilibili, KindVideo, "BV1xx411c7mD", 1, "https://www.bilibili.com/video/BV1xx411c7mD?p=1", true},
		{"https://space.bilibili.com/12345", PlatformBilibili, KindChannel, "12345", 0, "", true},
		{"https://example.com/some/page", "", "", "", 0, "", false},
		{"ftp://www.youtube.com/watch?v=abc123xxxxx", "", "", "", 0, "", false},
		{"not a url", "", "", "", 0, "", false},
		// YouTube 影片 ID 必須剛好 11 個字元（ING-R1）。
		{"https://www.youtube.com/watch?v=abc123xxxx", "", "", "", 0, "", false},
		{"https://www.youtube.com/watch?v=abc123xxxxxx", "", "", "", 0, "", false},
		{"https://youtu.be/abc123", "", "", "", 0, "", false},
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

func TestImplemented(t *testing.T) {
	for _, id := range []string{"youtube-manual", "youtube-auto", "bilibili-cc", "bilibili-ai", "local-sidecar", "local-embedded", "supadata", "groq"} {
		if !Implemented(id) {
			t.Errorf("%s 應該已實作", id)
		}
	}
	for _, id := range []string{"podcast-transcript", ""} {
		if Implemented(id) {
			t.Errorf("%s 還沒有實作", id)
		}
	}
	if got := ImplementedSources(); len(got) != 8 {
		t.Errorf("ImplementedSources = %v", got)
	}
}

func TestRef(t *testing.T) {
	tests := []struct {
		platform, id string
		part         int
		want         string
	}{
		{PlatformYouTube, "abc123xxxxx", 0, "yt:abc123xxxxx"},
		{PlatformBilibili, "BV1xx411c7mD", 3, "bili:BV1xx411c7mD/p3"},
		{PlatformBilibili, "BV1xx411c7mD", 0, "bili:BV1xx411c7mD/p1"}, // 未指定分P 時短代號仍是 p1
		{PlatformPodcast, "show/ep-42", 0, "pod:show/ep-42"},
		{PlatformLocal, "talk.mp4", 0, "local:talk.mp4"},
	}
	for _, tt := range tests {
		if got := Ref(tt.platform, tt.id, tt.part); got != tt.want {
			t.Errorf("Ref(%s, %s, %d) = %q，預期 %q", tt.platform, tt.id, tt.part, got, tt.want)
		}
	}
}
