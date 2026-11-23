package transcript

import "testing"

func TestRuleIndex(t *testing.T) {
	rules := []Rule{{Lang: OriginalLang, Kind: KindManual}, {Lang: OriginalLang, Kind: KindAuto}, {Lang: "zh-Hans", Kind: KindManual}}
	cases := []struct {
		t        Track
		orig     string
		allowTr  bool
		want     int
		describe string
	}{
		{Track{Lang: "en", Kind: KindManual}, "en", false, 0, "原文人工字幕"},
		{Track{Lang: "en-US", Kind: KindManual}, "en", false, 0, "主要語言相同"},
		{Track{Lang: "en", Kind: KindAuto}, "en", false, 1, "原文自動字幕"},
		{Track{Lang: "zh-Hans", Kind: KindManual}, "en", false, 2, "第三條"},
		{Track{Lang: "en", Kind: KindManual}, "", false, 3, "原文語言未知"},
		{Track{Lang: "en", Kind: KindPlatformTranslated}, "en", false, 3, "平台翻譯預設不採用"},
		{Track{Lang: "en", Kind: KindPlatformTranslated}, "en", true, 1, "允許平台翻譯時對應 auto 規則"},
		{Track{Lang: "fr", Kind: KindManual}, "en", false, 3, "沒有規則符合"},
		{Track{Lang: "", Kind: KindManual}, "en", false, 3, "沒有語言"},
	}
	for _, c := range cases {
		if got := RuleIndex(c.t, c.orig, rules, c.allowTr); got != c.want {
			t.Errorf("%s：RuleIndex = %d, want %d", c.describe, got, c.want)
		}
	}
	if got := RuleIndex(Track{Lang: "en", Kind: KindManual}, "en", nil, false); got != 0 {
		t.Errorf("沒有規則 = %d", got)
	}
}
