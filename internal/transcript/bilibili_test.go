package transcript

import (
	"reflect"
	"testing"
)

func TestPlatformSource(t *testing.T) {
	tests := []struct {
		platform, kind, want string
	}{
		{PlatformYouTube, KindManual, SourceYouTubeManual},
		{PlatformYouTube, KindAuto, SourceYouTubeAuto},
		{PlatformYouTube, KindPlatformTranslated, SourceYouTubeAuto},
		{PlatformBilibili, KindManual, SourceBilibiliCC},
		{PlatformBilibili, KindAuto, SourceBilibiliAI},
		// 沒有指定平台時維持 YouTube 的對應。
		{"", KindAuto, SourceYouTubeAuto},
	}
	for _, tt := range tests {
		if got := PlatformSource(tt.platform, tt.kind); got != tt.want {
			t.Errorf("PlatformSource(%q, %q) = %q，預期 %q", tt.platform, tt.kind, got, tt.want)
		}
	}
}

func TestBilibiliTrackMapping(t *testing.T) {
	tests := []struct {
		key  string
		want Track
		ok   bool
	}{
		{"zh-Hans", Track{Lang: "zh-Hans", Kind: KindManual}, true},
		{"en", Track{Lang: "en", Kind: KindManual}, true},
		{"ai-zh", Track{Lang: "zh", Kind: KindAuto}, true},
		{"ai-zh-Hans", Track{Lang: "zh-Hans", Kind: KindAuto}, true},
		{"danmaku", Track{}, false}, // 彈幕不是字幕
		{"ai-", Track{}, false},
		{"", Track{}, false},
	}
	for _, tt := range tests {
		got, ok := BilibiliTrack(tt.key)
		if ok != tt.ok || got != tt.want {
			t.Errorf("BilibiliTrack(%q) = %+v, %v；預期 %+v, %v", tt.key, got, ok, tt.want, tt.ok)
		}
		if ok {
			if back := BilibiliKey(got); back != tt.key {
				t.Errorf("BilibiliKey(%+v) = %q，預期 %q", got, back, tt.key)
			}
		}
	}
}

func TestBilibiliTracksSkipsDanmaku(t *testing.T) {
	got := BilibiliTracks([]string{"ai-zh", "danmaku", "en"})
	want := []Track{{Lang: "zh", Kind: KindAuto}, {Lang: "en", Kind: KindManual}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tracks = %+v", got)
	}
}

func TestSelectForBilibiliUsesBilibiliSources(t *testing.T) {
	tracks := []Track{{Lang: "zh", Kind: KindAuto}}
	got, decisions := SelectFor(PlatformBilibili, tracks, "zh", defaultRules, false)
	if got == nil || *got != (Track{Lang: "zh", Kind: KindAuto}) {
		t.Fatalf("chosen = %+v", got)
	}
	want := []Decision{
		{Source: SourceBilibiliCC, Lang: "zh", Kind: KindManual, State: StateUnavailable, Reason: ReasonNoTrack},
		{Source: SourceBilibiliAI, Lang: "zh", Kind: KindAuto, State: StateUsed},
	}
	if !reflect.DeepEqual(decisions, want) {
		t.Errorf("decisions = %+v", decisions)
	}

	// 原文語言不明時（Bilibili 沒有語言中繼資料），original 規則記錄對應的 Bilibili 來源。
	_, decisions = SelectFor(PlatformBilibili, nil, "", defaultRules[:2], false)
	if decisions[0].Source != SourceBilibiliCC || decisions[1].Source != SourceBilibiliAI {
		t.Errorf("decisions = %+v", decisions)
	}
}
