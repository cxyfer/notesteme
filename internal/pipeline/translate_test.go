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
	if a, ok := translateInput(arts, nil); !ok || a.ID != "c2" {
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
		if a, ok := translateInput(arts, nil); !ok || a.ID != want[name] {
			t.Errorf("%s：%+v, %v，預期 %s", name, a, ok, want[name])
		}
	}
	// correct 重跑（上游沒變）：新的 corrected 採用。
	if a, ok := translateInput([]store.Artifact{mk("n1", "normalized"), mk("s1", "segmented"), mk("c1", "corrected"), mk("c2", "corrected")}, nil); !ok || a.ID != "c2" {
		t.Errorf("correct 重跑：%+v, %v", a, ok)
	}
	if a, ok := translateInput([]store.Artifact{mk("n1", "normalized"), mk("s1", "segmented"), mk("t1", "translated")}, nil); !ok || a.ID != "s1" {
		t.Errorf("segmented 優先於 normalized：%+v, %v", a, ok)
	}
	if a, ok := translateInput([]store.Artifact{mk("n1", "normalized"), mk("n2", "normalized"), mk("src", "source")}, nil); !ok || a.ID != "n2" {
		t.Errorf("只有 normalized：%+v, %v", a, ok)
	}
	if _, ok := translateInput([]store.Artifact{mk("src", "source"), mk("t1", "translated"), mk("e1", "edited")}, nil); ok {
		t.Error("source、translated、edited 都不是 translate 的輸入")
	}
	if _, ok := translateInput(nil, nil); ok {
		t.Error("沒有產出")
	}
}

// 輸入依現行的 stage run 判斷（Codex P1）：最新的 run 是 skipped 的步驟，舊脈絡留下的產出不採用；
// 不比對建立時間：快取命中時 RestoreArtifact 保留原本的建立時間，上游還原的產出看起來比舊的下游產出更早。
func TestTranslateInputFollowsStageRuns(t *testing.T) {
	mk := func(id, variant string) store.Artifact {
		return store.Artifact{ID: id, Kind: "transcript", Variant: variant}
	}
	// n1 → s1 → c1 依序產生；從 normalize 重跑時 n1 被快取還原（仍排在最前面），下游兩步 skipped。
	arts := []store.Artifact{mk("n1", "normalized"), mk("s1", "segmented"), mk("c1", "corrected")}
	run := func(status string) store.StageRun { return store.StageRun{Status: status} }
	for _, tt := range []struct {
		name   string
		latest map[string]store.StageRun
		want   string
	}{
		{"correct 與 segment 都 skipped：用還原的 normalized", map[string]store.StageRun{StageNormalize: run("done"), StageSegment: run("skipped"), StageCorrect: run("skipped")}, "n1"},
		{"只有 correct skipped：用 segmented", map[string]store.StageRun{StageNormalize: run("done"), StageSegment: run("done"), StageCorrect: run("skipped")}, "s1"},
		{"都 done：用 corrected（不看建立順序）", map[string]store.StageRun{StageNormalize: run("done"), StageSegment: run("done"), StageCorrect: run("done")}, "c1"},
		{"segment skipped、correct done：用 corrected", map[string]store.StageRun{StageNormalize: run("done"), StageSegment: run("skipped"), StageCorrect: run("done")}, "c1"},
	} {
		if a, ok := translateInput(arts, tt.latest); !ok || a.ID != tt.want {
			t.Errorf("%s：%+v, %v，預期 %s", tt.name, a, ok, tt.want)
		}
	}
	// 沒有 normalized 之外的選擇、也沒有任何產出。
	if _, ok := translateInput([]store.Artifact{mk("c1", "corrected")}, map[string]store.StageRun{StageCorrect: run("skipped")}); ok {
		t.Error("correct skipped 而且沒有其他產出：不該有輸入")
	}
}
