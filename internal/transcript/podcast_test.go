package transcript

import (
	"reflect"
	"testing"
)

func pt(lang, format, handle string) Track {
	return Track{Lang: lang, Kind: KindPodcast, Format: format, Handle: handle}
}

func TestSelectPodcast(t *testing.T) {
	tests := []struct {
		name     string
		tracks   []Track
		original string
		rules    []Rule
		want     string // 選中軌的 Handle，空字串代表沒有
		dec      Decision
	}{
		{
			name: "沒有字幕軌", original: "en", rules: defaultRules,
			dec: Decision{Source: SourcePodcastTranscript, Kind: KindPodcast, State: StateUnavailable, Reason: ReasonNoTrack},
		},
		{
			name: "原文語言符合", tracks: []Track{pt("en", "vtt", "a")}, original: "en", rules: defaultRules, want: "a",
			dec: Decision{Source: SourcePodcastTranscript, Lang: "en", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "依 caption_priority：原文先於 zh-Hans", tracks: []Track{pt("zh-Hans", "vtt", "zh"), pt("en", "json", "en")}, original: "en",
			rules: defaultRules, want: "en", dec: Decision{Source: SourcePodcastTranscript, Lang: "en", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "原文語言沒有字幕，改用 zh-Hans", tracks: []Track{pt("zh-Hans", "srt", "zh"), pt("fr", "vtt", "fr")}, original: "ja",
			rules: defaultRules, want: "zh", dec: Decision{Source: SourcePodcastTranscript, Lang: "zh-Hans", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "同層依格式 vtt > srt > json", tracks: []Track{pt("en", "json", "j"), pt("en", "srt", "s"), pt("en", "vtt", "v")}, original: "en",
			rules: defaultRules, want: "v", dec: Decision{Source: SourcePodcastTranscript, Lang: "en", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "格式相同依出現順序", tracks: []Track{pt("en", "vtt", "first"), pt("en", "vtt", "second")}, original: "en",
			rules: defaultRules, want: "first", dec: Decision{Source: SourcePodcastTranscript, Lang: "en", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "地區變體算同一主要語言", tracks: []Track{pt("en-US", "vtt", "us")}, original: "en",
			rules: defaultRules, want: "us", dec: Decision{Source: SourcePodcastTranscript, Lang: "en-US", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "語言都不符 caption_priority：語言未知的軌仍可用", tracks: []Track{pt("fr", "vtt", "fr"), pt("", "srt", "unknown")}, original: "en",
			rules: defaultRules, want: "unknown", dec: Decision{Source: SourcePodcastTranscript, Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "語言都不符：主要語言與原文相同的軌", tracks: []Track{pt("fr", "vtt", "fr"), pt("en-GB", "vtt", "gb")}, original: "en-US",
			rules: []Rule{{Lang: "zh-Hans", Kind: KindManual}}, want: "gb", dec: Decision{Source: SourcePodcastTranscript, Lang: "en-GB", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "語言都不符原文", tracks: []Track{pt("fr", "vtt", "fr"), pt("de", "srt", "de")}, original: "en",
			rules: defaultRules, dec: Decision{Source: SourcePodcastTranscript, Lang: "fr", Kind: KindPodcast, State: StateRejected, Reason: ReasonLanguageMismatch},
		},
		{
			name: "原文語言未知：任何軌都可用，之後由它推斷", tracks: []Track{pt("ja", "vtt", "ja")}, original: "",
			rules: defaultRules, want: "ja", dec: Decision{Source: SourcePodcastTranscript, Lang: "ja", Kind: KindPodcast, State: StateUsed},
		},
		{
			name: "忽略不是 podcast 的軌", tracks: []Track{{Lang: "en", Kind: KindSidecar, Handle: "x"}}, original: "en",
			rules: defaultRules, dec: Decision{Source: SourcePodcastTranscript, Kind: KindPodcast, State: StateUnavailable, Reason: ReasonNoTrack},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chosen, decisions := SelectPodcast(tt.tracks, tt.original, tt.rules)
			got := ""
			if chosen != nil {
				got = chosen.Handle
			}
			if got != tt.want {
				t.Errorf("chosen = %q，預期 %q", got, tt.want)
			}
			if !reflect.DeepEqual(decisions, []Decision{tt.dec}) {
				t.Errorf("decisions = %+v，預期 %+v", decisions, tt.dec)
			}
		})
	}
}

func TestPlatformSourceForPodcast(t *testing.T) {
	for _, kind := range []string{KindPodcast, "", KindManual} {
		if got := PlatformSource(PlatformPodcast, kind); got != SourcePodcastTranscript {
			t.Errorf("PlatformSource(podcast, %q) = %q", kind, got)
		}
	}
}
