package faketool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 平台事後補上的字幕軌（SRC-10）：fake clock 到了才列出、才能下載。
func TestLaterTracksFollowFakeClock(t *testing.T) {
	st := sampleState(t)
	st.Videos["late02xxxxx"] = &Video{Title: "L", Language: "en", Tracks: []Track{{Kind: KindAuto, Lang: "en"}},
		Later: []Track{{Kind: KindManual, Lang: "en", AvailableAt: "2026-11-21T00:00:00Z"}, {Kind: KindManual, Lang: "fr", AvailableAt: "bad"}}}
	s := newScenario(t, st)
	info := func() map[string]any {
		t.Helper()
		code, out, errs := s.inproc("yt-dlp", "-J", "--no-playlist", "https://www.youtube.com/watch?v=late02xxxxx")
		if code != 0 {
			t.Fatalf("code=%d stderr=%q", code, errs)
		}
		var v struct {
			Subtitles map[string]any `json:"subtitles"`
		}
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Fatal(err)
		}
		return v.Subtitles
	}
	// 沒有 now 檔：還沒補上。
	if subs := info(); len(subs) != 0 {
		t.Fatalf("沒有 now 檔：%v", subs)
	}
	if err := WriteNow(s.root, time.Date(2026, 11, 20, 23, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if subs := info(); len(subs) != 0 {
		t.Fatalf("補上之前：%v", subs)
	}
	if err := WriteNow(s.root, time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if subs := info(); len(subs) != 1 || subs["en"] == nil {
		t.Fatalf("補上之後（格式不對的時間不列出）：%v", subs)
	}
	code, _, errs := s.inproc("yt-dlp", "--skip-download", "--write-subs", "--sub-langs", "en",
		"--sub-format", "vtt", "-o", "subs/%(id)s.%(ext)s", "https://youtu.be/late02xxxxx")
	if code != 0 {
		t.Fatalf("下載：code=%d stderr=%q", code, errs)
	}
	if _, err := os.Stat(filepath.Join(s.work, "subs", "late02xxxxx.en.vtt")); err != nil {
		t.Fatal(err)
	}
	// now 檔格式不對：視為還沒補上。
	if err := os.WriteFile(NowFile(s.root), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if subs := info(); len(subs) != 0 {
		t.Fatalf("格式不對的 now：%v", subs)
	}
	// 取代同種類、同語言的現有字幕軌。
	v := &Video{Tracks: []Track{{Kind: KindManual, Lang: "en", Cues: 1}, {Kind: KindAuto, Lang: "en"}},
		Later: []Track{{Kind: KindManual, Lang: "en", AvailableAt: "2026-11-21T00:00:00Z"}}}
	got := v.at(time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC))
	if len(got.Tracks) != 2 || got.Tracks[1].Kind != KindManual || got.Tracks[1].Cues != 0 || got.Tracks[1].AvailableAt != "" || len(v.Tracks) != 2 || v.Tracks[0].Cues != 1 {
		t.Fatalf("tracks = %+v（原本 %+v）", got.Tracks, v.Tracks)
	}
}
