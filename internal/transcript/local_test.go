package transcript

import (
	"encoding/json"
	"reflect"
	"testing"
)

func side(lang, path string) Track { return Track{Lang: lang, Kind: KindSidecar, Handle: path} }
func emb(lang, idx string, def, forced bool) Track {
	return Track{Lang: lang, Kind: KindEmbedded, Handle: idx, Default: def, Forced: forced}
}

func TestSelectLocal(t *testing.T) {
	tests := []struct {
		name     string
		tracks   []Track
		original string
		rules    []Rule
		want     *Track
		wantDec  []Decision
	}{
		{
			name:   "sidecar 優先於內嵌字幕",
			tracks: []Track{emb("en", "2", true, false), side("en", "/d/talk.en.srt")},
			rules:  defaultRules, original: "en",
			want:    &Track{Lang: "en", Kind: KindSidecar, Handle: "/d/talk.en.srt"},
			wantDec: []Decision{{Source: SourceLocalSidecar, Lang: "en", Kind: KindSidecar, State: StateUsed}},
		},
		{
			name:   "語言優先：original 的 sidecar 先於其他語言",
			tracks: []Track{side("ja", "/d/talk.ja.srt"), side("en", "/d/talk.en.vtt")},
			rules:  defaultRules, original: "en",
			want:    &Track{Lang: "en", Kind: KindSidecar, Handle: "/d/talk.en.vtt"},
			wantDec: []Decision{{Source: SourceLocalSidecar, Lang: "en", Kind: KindSidecar, State: StateUsed}},
		},
		{
			name:   "caption_priority 的第二條規則",
			tracks: []Track{side("ja", "/d/talk.ja.srt"), side("zh-Hans", "/d/talk.zh-Hans.srt")},
			rules:  defaultRules, original: "en",
			want:    &Track{Lang: "zh-Hans", Kind: KindSidecar, Handle: "/d/talk.zh-Hans.srt"},
			wantDec: []Decision{{Source: SourceLocalSidecar, Lang: "zh-Hans", Kind: KindSidecar, State: StateUsed}},
		},
		{
			name:   "同語言：.srt 先於 .vtt",
			tracks: []Track{side("en", "/d/talk.en.vtt"), side("en", "/d/talk.en.srt")},
			rules:  defaultRules, original: "en",
			want:    &Track{Lang: "en", Kind: KindSidecar, Handle: "/d/talk.en.srt"},
			wantDec: []Decision{{Source: SourceLocalSidecar, Lang: "en", Kind: KindSidecar, State: StateUsed}},
		},
		{
			name:   "語言都不在清單中：.srt 先於 .vtt，再依檔名",
			tracks: []Track{side("fr", "/d/talk.fr.vtt"), side("de", "/d/talk.de.srt"), side("es", "/d/talk.es.srt")},
			rules:  defaultRules, original: "en",
			want:    &Track{Lang: "de", Kind: KindSidecar, Handle: "/d/talk.de.srt"},
			wantDec: []Decision{{Source: SourceLocalSidecar, Lang: "de", Kind: KindSidecar, State: StateUsed}},
		},
		{
			name:   "原文語言不明：original 規則略過，仍取固定順序的第一條",
			tracks: []Track{side("ja", "/d/talk.ja.vtt"), side("en", "/d/talk.en.srt")},
			rules:  defaultRules, original: "",
			want:    &Track{Lang: "en", Kind: KindSidecar, Handle: "/d/talk.en.srt"},
			wantDec: []Decision{{Source: SourceLocalSidecar, Lang: "en", Kind: KindSidecar, State: StateUsed}},
		},
		{
			name:   "沒有 sidecar：用內嵌字幕，並記下 sidecar 沒有",
			tracks: []Track{emb("en", "3", false, false)},
			rules:  defaultRules, original: "en",
			want: &Track{Lang: "en", Kind: KindEmbedded, Handle: "3"},
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Lang: "en", Kind: KindEmbedded, State: StateUsed},
			},
		},
		{
			name:   "內嵌：default 的先",
			tracks: []Track{emb("fr", "2", false, false), emb("de", "3", true, false)},
			rules:  nil, original: "",
			want: &Track{Lang: "de", Kind: KindEmbedded, Handle: "3", Default: true},
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Lang: "de", Kind: KindEmbedded, State: StateUsed},
			},
		},
		{
			name:   "內嵌：同為 default 時非 forced 的先",
			tracks: []Track{emb("fr", "2", true, true), emb("de", "5", true, false)},
			rules:  nil, original: "",
			want: &Track{Lang: "de", Kind: KindEmbedded, Handle: "5", Default: true},
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Lang: "de", Kind: KindEmbedded, State: StateUsed},
			},
		},
		{
			name:   "內嵌：旗標相同時依串流 index（數值，不是字串順序）",
			tracks: []Track{emb("fr", "10", false, false), emb("de", "9", false, false)},
			rules:  nil, original: "",
			want: &Track{Lang: "de", Kind: KindEmbedded, Handle: "9"},
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Lang: "de", Kind: KindEmbedded, State: StateUsed},
			},
		},
		{
			name:   "內嵌：語言優先於 default",
			tracks: []Track{emb("fr", "2", true, false), emb("en", "3", false, false)},
			rules:  defaultRules, original: "en",
			want: &Track{Lang: "en", Kind: KindEmbedded, Handle: "3"},
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Lang: "en", Kind: KindEmbedded, State: StateUsed},
			},
		},
		{
			name:   "內嵌字幕沒有語言標記：仍可採用，語言為空",
			tracks: []Track{emb("", "2", true, false)},
			rules:  defaultRules, original: "en",
			want: &Track{Lang: "", Kind: KindEmbedded, Handle: "2", Default: true},
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Kind: KindEmbedded, State: StateUsed},
			},
		},
		{
			name:   "都沒有：兩種都記 no_track",
			tracks: nil,
			rules:  defaultRules, original: "en",
			want: nil,
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Kind: KindEmbedded, State: StateUnavailable, Reason: ReasonNoTrack},
			},
		},
		{
			name:   "別種的軌被忽略",
			tracks: []Track{{Lang: "en", Kind: KindManual}},
			rules:  defaultRules, original: "en",
			want: nil,
			wantDec: []Decision{
				{Source: SourceLocalSidecar, Kind: KindSidecar, State: StateUnavailable, Reason: ReasonNoTrack},
				{Source: SourceLocalEmbedded, Kind: KindEmbedded, State: StateUnavailable, Reason: ReasonNoTrack},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, dec := SelectLocal(tt.tracks, tt.original, tt.rules)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chosen = %+v，預期 %+v", got, tt.want)
			}
			if !reflect.DeepEqual(dec, tt.wantDec) {
				t.Errorf("decisions = %+v，預期 %+v", dec, tt.wantDec)
			}
		})
	}
}

func TestSelectLocalDoesNotReorderInput(t *testing.T) {
	in := []Track{side("en", "/d/b.en.vtt"), side("en", "/d/a.en.srt")}
	SelectLocal(in, "en", defaultRules)
	if in[0].Handle != "/d/b.en.vtt" {
		t.Errorf("輸入被改動：%+v", in)
	}
}

func TestPlatformSourceLocal(t *testing.T) {
	if got := PlatformSource(PlatformLocal, KindSidecar); got != SourceLocalSidecar {
		t.Errorf("sidecar = %s", got)
	}
	if got := PlatformSource(PlatformLocal, KindEmbedded); got != SourceLocalEmbedded {
		t.Errorf("embedded = %s", got)
	}
}

// Track 新增的欄位都是 omitempty：YouTube 與 Bilibili 的字幕軌 marshal 結果不變（快取鍵不變）。
func TestTrackJSONOmitsLocalFields(t *testing.T) {
	b, err := json.Marshal([]Track{{Lang: "en", Kind: KindManual}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"Lang":"en","Kind":"manual"}]`; string(b) != want {
		t.Errorf("json = %s，預期 %s", b, want)
	}
	b, _ = json.Marshal(Track{Lang: "en", Kind: KindEmbedded, Handle: "2", Default: true})
	if want := `{"Lang":"en","Kind":"embedded","Handle":"2","Default":true}`; string(b) != want {
		t.Errorf("json = %s，預期 %s", b, want)
	}
}
