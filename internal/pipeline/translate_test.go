package pipeline

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

func TestPlanTargets(t *testing.T) {
	type want = TargetPlan
	cases := []struct {
		name     string
		source   string
		lang     Language
		expected []want
	}{
		{"簡體 → 台灣繁體", "zh-Hans", Language{Targets: []string{"zh-Hant-TW"}, VariantStrategy: "convert"},
			[]want{{"zh-Hant-TW", TargetConvert, zhconv.S2TWP}}},
		{"strategy 空字串視為 convert", "zh-Hans", Language{Targets: []string{"zh-Hant-HK"}},
			[]want{{"zh-Hant-HK", TargetConvert, zhconv.S2HK}}},
		{"localize 的前置轉換不做詞彙轉換", "zh-Hans", Language{Targets: []string{"zh-Hant-TW"}, VariantStrategy: "localize"},
			[]want{{"zh-Hant-TW", TargetLocalize, zhconv.S2TW}}},
		{"繁體 → 簡體", "zh-Hant", Language{Targets: []string{"zh-Hans"}, VariantStrategy: "convert"},
			[]want{{"zh-Hans", TargetConvert, zhconv.T2S}}},
		{"相同與去重（順序不變）", "zh-Hans", Language{Targets: []string{"zh-Hans", "zh-Hant-TW", "zh_hant_tw", "zh-Hant-HK"}},
			[]want{{"zh-Hans", TargetSame, ""}, {"zh-Hant-TW", TargetConvert, zhconv.S2TWP}, {"zh-Hant-HK", TargetConvert, zhconv.S2HK}}},
		{"只差字形而沒有轉換表視為相同", "zh-Hant-TW", Language{Targets: []string{"zh-Hant"}},
			[]want{{"zh-Hant", TargetSame, ""}}},
		{"非中文需要翻譯", "zh-Hans", Language{Targets: []string{"en", "zh-Hant-TW"}},
			[]want{{"en", TargetTranslate, ""}, {"zh-Hant-TW", TargetConvert, zhconv.S2TWP}}},
		{"英文 → 中文需要翻譯", "en", Language{Targets: []string{"zh-Hant-TW"}},
			[]want{{"zh-Hant-TW", TargetTranslate, ""}}},
		{"原文是 zh：字形不明需要翻譯", "zh", Language{Targets: []string{"zh-Hant-TW"}},
			[]want{{"zh-Hant-TW", TargetTranslate, ""}}},
		{"原文是 zh 而目標也是 zh：相同", "zh", Language{Targets: []string{"zh"}},
			[]want{{"zh", TargetSame, ""}}},
		{"原文語言不明", "", Language{Targets: []string{"zh-Hant-TW"}},
			[]want{{"zh-Hant-TW", TargetTranslate, ""}}},
	}
	for _, c := range cases {
		got := PlanTargets(c.source, c.lang)
		if len(got) != len(c.expected) {
			t.Errorf("%s：%+v，預期 %+v", c.name, got, c.expected)
			continue
		}
		for i := range got {
			if got[i] != c.expected[i] {
				t.Errorf("%s：[%d] = %+v，預期 %+v", c.name, i, got[i], c.expected[i])
			}
		}
	}
}

func TestTranslateInputPriority(t *testing.T) {
	mk := func(id, variant string) store.Artifact {
		return store.Artifact{ID: id, Kind: "transcript", Variant: variant}
	}
	arts := []store.Artifact{
		mk("n1", "normalized"), mk("src", "source"), mk("s1", "segmented"), mk("c1", "corrected"), mk("c2", "corrected"), mk("t1", "translated"),
	}
	if a, ok := translateInput(arts); !ok || a.ID != "c2" {
		t.Errorf("corrected 優先，而且取最近產生的：%+v, %v", a, ok)
	}
	// 從上游重跑之後，舊的下游產出（同一種變體才會被取代）屬於被取代的脈絡，不採用（Codex P1）。
	stale := map[string][]store.Artifact{
		"normalize 重跑後，舊的 corrected 與 segmented 不採用": {mk("n1", "normalized"), mk("s1", "segmented"), mk("c1", "corrected"), mk("n2", "normalized")},
		"segment 重跑後，舊的 corrected 不採用":               {mk("n1", "normalized"), mk("s1", "segmented"), mk("c1", "corrected"), mk("s2", "segmented")},
	}
	want := map[string]string{
		"normalize 重跑後，舊的 corrected 與 segmented 不採用": "n2",
		"segment 重跑後，舊的 corrected 不採用":               "s2",
	}
	for name, arts := range stale {
		if a, ok := translateInput(arts); !ok || a.ID != want[name] {
			t.Errorf("%s：%+v, %v，預期 %s", name, a, ok, want[name])
		}
	}
	// correct 重跑（上游沒變）：新的 corrected 採用。
	if a, ok := translateInput([]store.Artifact{mk("n1", "normalized"), mk("s1", "segmented"), mk("c1", "corrected"), mk("c2", "corrected")}); !ok || a.ID != "c2" {
		t.Errorf("correct 重跑：%+v, %v", a, ok)
	}
	if a, ok := translateInput([]store.Artifact{mk("n1", "normalized"), mk("s1", "segmented"), mk("t1", "translated")}); !ok || a.ID != "s1" {
		t.Errorf("segmented 優先於 normalized：%+v, %v", a, ok)
	}
	if a, ok := translateInput([]store.Artifact{mk("n1", "normalized"), mk("n2", "normalized"), mk("src", "source")}); !ok || a.ID != "n2" {
		t.Errorf("只有 normalized：%+v, %v", a, ok)
	}
	if _, ok := translateInput([]store.Artifact{mk("src", "source"), mk("t1", "translated"), mk("e1", "edited")}); ok {
		t.Error("source、translated、edited 都不是 translate 的輸入")
	}
	if _, ok := translateInput(nil); ok {
		t.Error("沒有產出")
	}
}
