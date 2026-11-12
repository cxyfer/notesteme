package steps

import (
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

func TestRefToURL(t *testing.T) {
	tests := map[string]string{
		"yt:abc123":                "https://www.youtube.com/watch?v=abc123",
		"bili:BV1xx411c7mD/p2":     "https://www.bilibili.com/video/BV1xx411c7mD?p=2",
		"https://youtu.be/abc123":  "https://youtu.be/abc123",
		"https://example.com/page": "https://example.com/page",
	}
	for in, want := range tests {
		if got := refToURL(in); got != want {
			t.Errorf("refToURL(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestRefPatternFindsRefsInPaths(t *testing.T) {
	tests := map[string]string{
		"/api/items/yt:abc123":               "yt:abc123",
		"/api/items/yt:abc123/cues?lang=en":  "yt:abc123",
		"/api/items/bili:BV1xx411c7mD/p3":    "bili:BV1xx411c7mD/p3",
		"/api/channels/channel-of:yt:abc123": "channel-of:yt:abc123",
		"/api/items/pod:show/ep-42":          "pod:show/ep-42",
	}
	for in, want := range tests {
		if got := refPattern.FindString(in); got != want {
			t.Errorf("%q：找到 %q，預期 %q", in, got, want)
		}
	}
	if refPattern.MatchString("/api/items/0190a1b2-0000-7000-8000-000000000000") {
		t.Error("UUID 不是影音代號")
	}
}

func TestParseChapters(t *testing.T) {
	got, err := parseChapters("00:00 Intro; 05:00 Main; 12:00 Q&A", 900)
	if err != nil {
		t.Fatal(err)
	}
	want := []faketool.Chapter{{Title: "Intro", Start: 0, End: 300}, {Title: "Main", Start: 300, End: 720}, {Title: "Q&A", Start: 720, End: 900}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("chapters = %+v", got)
	}
	for _, bad := range []string{"Intro", "xx:00 A", "1:2:3:4 A"} {
		if _, err := parseChapters(bad, 0); err == nil {
			t.Errorf("%q 應回報錯誤", bad)
		}
	}
}

func TestSetVideoFieldRejectsUnknownFieldsAndBadValues(t *testing.T) {
	v := &faketool.Video{}
	for field, value := range map[string]string{"title": "T", "duration": "600", "language": "en", "published_at": "2026-11-12T09:30:00Z", "live_status": "is_live"} {
		if err := setVideoField(v, field, value); err != nil {
			t.Errorf("%s：%v", field, err)
		}
	}
	if v.Duration != 600 || v.LiveStatus != "is_live" {
		t.Errorf("video = %+v", v)
	}
	for field, value := range map[string]string{"bogus": "x", "duration": "abc", "published_at": "yesterday"} {
		if err := setVideoField(v, field, value); err == nil {
			t.Errorf("%s=%q 應回報錯誤", field, value)
		}
	}
}
