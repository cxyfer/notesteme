package transcript

import (
	"reflect"
	"testing"
)

var defaultRules = []Rule{
	{Lang: "original", Kind: KindManual},
	{Lang: "original", Kind: KindAuto},
	{Lang: "zh-Hans", Kind: KindManual},
}

func TestSelectPrefersManualInOriginalLanguage(t *testing.T) {
	tracks := []Track{{Lang: "en", Kind: KindAuto}, {Lang: "en", Kind: KindManual}, {Lang: "zh-Hans", Kind: KindManual}}
	got, decisions := Select(tracks, "en", defaultRules, false)
	if got == nil || *got != (Track{Lang: "en", Kind: KindManual}) {
		t.Fatalf("chosen = %+v", got)
	}
	want := []Decision{{Source: SourceYouTubeManual, Lang: "en", Kind: KindManual, State: StateUsed}}
	if !reflect.DeepEqual(decisions, want) {
		t.Errorf("decisions = %+v", decisions)
	}
}

func TestSelectFallsDownThePriorityList(t *testing.T) {
	got, decisions := Select([]Track{{Lang: "zh-Hans", Kind: KindManual}}, "ja", defaultRules, false)
	if got == nil || got.Lang != "zh-Hans" || got.Kind != KindManual {
		t.Fatalf("chosen = %+v", got)
	}
	want := []Decision{
		{Source: SourceYouTubeManual, Lang: "ja", Kind: KindManual, State: StateUnavailable, Reason: ReasonNoTrack},
		{Source: SourceYouTubeAuto, Lang: "ja", Kind: KindAuto, State: StateUnavailable, Reason: ReasonNoTrack},
		{Source: SourceYouTubeManual, Lang: "zh-Hans", Kind: KindManual, State: StateUsed},
	}
	if !reflect.DeepEqual(decisions, want) {
		t.Errorf("decisions = %+v", decisions)
	}
}

func TestSelectAutoWhenNoManual(t *testing.T) {
	got, _ := Select([]Track{{Lang: "en", Kind: KindAuto}}, "en", defaultRules, false)
	if got == nil || got.Kind != KindAuto {
		t.Fatalf("chosen = %+v", got)
	}
}

func TestSelectIgnoresPlatformTranslatedByDefault(t *testing.T) {
	tracks := []Track{{Lang: "zh-Hans", Kind: KindPlatformTranslated}, {Lang: "fr", Kind: KindPlatformTranslated}}
	rules := []Rule{{Lang: "original", Kind: KindAuto}, {Lang: "zh-Hans", Kind: KindAuto}}
	got, decisions := Select(tracks, "en", rules, false)
	if got != nil {
		t.Fatalf("機器翻譯預設不採用：%+v", got)
	}
	last := decisions[len(decisions)-1]
	if last.State != StateRejected || last.Reason != ReasonPlatformTranslated || last.Kind != KindPlatformTranslated {
		t.Errorf("應記錄機器翻譯被排除：%+v", decisions)
	}
	n := 0
	for _, d := range decisions {
		if d.Reason == ReasonPlatformTranslated {
			n++
		}
	}
	if n != 1 {
		t.Errorf("機器翻譯只記錄一筆，實際 %d", n)
	}

	// allow_platform_translated 打開後，kind 為 auto 的規則可以選用。
	got, _ = Select(tracks, "en", rules, true)
	if got == nil || got.Lang != "zh-Hans" || got.Kind != KindPlatformTranslated {
		t.Errorf("允許時應採用：%+v", got)
	}
}

func TestSelectOriginalLanguageUnknown(t *testing.T) {
	got, decisions := Select([]Track{{Lang: "en", Kind: KindManual}}, "", defaultRules[:2], false)
	if got != nil {
		t.Fatalf("不知道原文語言時 original 不符合：%+v", got)
	}
	if len(decisions) != 2 || decisions[0].Reason != ReasonOriginalLangUnknown {
		t.Errorf("decisions = %+v", decisions)
	}
}

func TestSelectLanguageMatching(t *testing.T) {
	tests := []struct {
		name   string
		tracks []Track
		orig   string
		want   string // 預期的 Lang；空字串代表沒有符合
	}{
		{"主要語言對地區變體", []Track{{Lang: "en-US", Kind: KindManual}}, "en", "en-US"},
		{"完全相同優先", []Track{{Lang: "en-US", Kind: KindManual}, {Lang: "en", Kind: KindManual}}, "en", "en"},
		{"同分時取輸入順序在前的", []Track{{Lang: "zh-Hans", Kind: KindManual}, {Lang: "zh-Hant", Kind: KindManual}}, "zh", "zh-Hans"},
		{"-orig 視為原文自動字幕", []Track{{Lang: "en-orig", Kind: KindAuto}}, "en", "en-orig"},
		{"不分大小寫", []Track{{Lang: "EN", Kind: KindManual}}, "en", "EN"},
		{"zh-Hans 不符合 zh-Hant", []Track{{Lang: "zh-Hant", Kind: KindManual}}, "zh-Hans", ""},
		{"英文不符合 es", []Track{{Lang: "es", Kind: KindManual}}, "en", ""},
	}
	for _, tt := range tests {
		got, _ := Select(tt.tracks, tt.orig, []Rule{{Lang: "original", Kind: KindManual}, {Lang: "original", Kind: KindAuto}}, false)
		switch {
		case tt.want == "" && got != nil:
			t.Errorf("%s：不應有符合，實際 %+v", tt.name, got)
		case tt.want != "" && (got == nil || got.Lang != tt.want):
			t.Errorf("%s：預期 %s，實際 %+v", tt.name, tt.want, got)
		}
	}
}

func TestSelectExplicitLanguageNeedsExactMatch(t *testing.T) {
	// 規則寫明 zh-Hans 時，不會用 zh-Hant 湊數。
	got, _ := Select([]Track{{Lang: "zh-Hant", Kind: KindManual}}, "ja", []Rule{{Lang: "zh-Hans", Kind: KindManual}}, false)
	if got != nil {
		t.Errorf("chosen = %+v", got)
	}
}

func TestNormalizeLang(t *testing.T) {
	if NormalizeLang("en-orig") != "en" || NormalizeLang("zh-Hans") != "zh-Hans" {
		t.Error("NormalizeLang")
	}
}

func TestDecisionsAreDeduplicated(t *testing.T) {
	rules := []Rule{{Lang: "original", Kind: KindManual}, {Lang: "en", Kind: KindManual}}
	_, decisions := Select(nil, "en", rules, false)
	if len(decisions) != 1 {
		t.Errorf("相同的來源與語言只記錄一次：%+v", decisions)
	}
}
