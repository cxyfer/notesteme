package pipeline

import (
	"context"
	"slices"

	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/zhconv"
)

// TargetKind 是一個目標語言的處理方式（TRN-R8～R10）。
type TargetKind string

const (
	// TargetSame：與原文相同，或只差字形而沒有轉換表（S6-Q3）；不需要處理。
	TargetSame TargetKind = "same"
	// TargetConvert：中文字形之間用 OpenCC 轉換（TRN-R8）。
	TargetConvert TargetKind = "convert"
	// TargetLocalize：先用 OpenCC 轉字形，再由 LLM 修正用詞（TRN-R9，S6b）。
	TargetLocalize TargetKind = "localize"
	// TargetTranslate：需要 LLM 翻譯（S6b）。
	TargetTranslate TargetKind = "translate"
)

// TargetPlan 是一個目標語言的處理方式。
type TargetPlan struct {
	Lang string
	Kind TargetKind
	// Table 只在 Convert、Localize 時有值；Localize 的轉換表不含詞彙轉換（例如台灣用 s2tw）。
	Table zhconv.Table
}

// PlanTargets 依原文語言與 language 設定決定每個目標語言的處理方式。目標去重（語言標記正規化後相同的只留第一個），順序不變。
// TargetPlan.Lang 是正規化後的標記（zh_hant_tw → zh-Hant-TW）：產出的 lang、快取鍵與取代都用同一個拼法，
// 否則大小寫不同的設定會留下兩份現行譯文（Codex P2）。
// sourceLang 為空代表不知道原文語言：每個目標都需要翻譯。
func PlanTargets(sourceLang string, lang Language) []TargetPlan {
	strategy := zhconv.StrategyConvert
	if lang.VariantStrategy == string(zhconv.StrategyLocalize) {
		strategy = zhconv.StrategyLocalize
	}
	var plans []TargetPlan
	seen := map[string]bool{}
	for _, target := range lang.Targets {
		key := langtag.Parse(target).String()
		if seen[key] {
			continue
		}
		seen[key] = true
		plan := planTarget(sourceLang, target, strategy)
		plan.Lang = key
		plans = append(plans, plan)
	}
	return plans
}

func planTarget(source, target string, strategy zhconv.Strategy) TargetPlan {
	p := TargetPlan{Lang: target, Kind: TargetTranslate}
	switch {
	case langtag.Equal(source, target):
		p.Kind = TargetSame
	case langtag.IsChinese(source) && langtag.IsChinese(target) && differentChineseLanguage(source, target):
		// 雙方明確指定不同的 extlang（zh-cmn 與 zh-yue）：是不同的語言，不只是字形，OpenCC 處理不了。
	case langtag.IsChinese(source) && langtag.IsChinese(target) && langtag.ChineseScript(source) != "":
		// 原文的字形已知：有轉換表就轉換；沒有（同一字形，或目標沒有指定字形）就視為相同語言（S6-Q3）。
		if t, ok := zhconv.TableFor(source, target, strategy); ok {
			p.Kind, p.Table = TargetConvert, t
			if strategy == zhconv.StrategyLocalize {
				p.Kind = TargetLocalize
			}
		} else {
			p.Kind = TargetSame
		}
	}
	return p
}

// differentChineseLanguage 回報兩個中文標記是否都明確指定了 extlang、而且不同。
func differentChineseLanguage(a, b string) bool {
	ea, eb := langtag.Parse(a).Ext, langtag.Parse(b).Ext
	return ea != "" && eb != "" && ea != eb
}

// allConvert 回報不是 same 的目標是否全部都是 convert（而且至少有一個）。
func allConvert(plans []TargetPlan) bool {
	n := 0
	for _, t := range plans {
		switch t.Kind {
		case TargetSame:
		case TargetConvert:
			n++
		default:
			return false
		}
	}
	return n > 0
}

// translateInput 選 translate 的輸入：現行產出中 corrected > segmented > normalized（COR-R8），同一種變體取最近產生的
// （arts 依建立順序排列，見 store.CurrentArtifacts）。
//
// 產出被取代的範圍是同一種變體，所以從上游步驟重跑（例如 correct 已經 off 或沒有設定，從 normalize 重跑）之後，
// 舊的 corrected、segmented 仍然是現行產出，要靠現行的 stage run 判斷它們還算不算數（Codex P1）：
//   - latest 是影音各步驟最新的 stage run：最新的 run 不是 done（skipped〔off、not_configured〕，或重跑停在 until 之前
//     而還是 pending），代表這個變體的步驟在現行脈絡中沒有產出，留下的是舊脈絡的檔案，不採用。不比對產出的建立時間：快取命中時 RestoreArtifact 保留原本的建立時間，
//     上游被快取還原的產出看起來比舊的下游產出更早，但它屬於現行脈絡。
//   - from 非空（Rerun 在寫入前解析執行方式）：from 之後（含）的步驟即將被重設，它們現行的產出屬於舊脈絡，不採用。
//   - latest 是空的（沒有 stage run 的資料，例如單元測試）：退回用建立順序判斷，比上游變體的最新產出更早的下游產出不採用。
func translateInput(arts []store.Artifact, latest map[string]store.StageRun, from string) (store.Artifact, bool) {
	pos := map[subtitle.Variant]int{subtitle.VariantNormalized: -1, subtitle.VariantSegmented: -1, subtitle.VariantCorrected: -1}
	for i, a := range arts {
		if a.Kind != "transcript" {
			continue
		}
		if _, ok := pos[subtitle.Variant(a.Variant)]; ok {
			pos[subtitle.Variant(a.Variant)] = i
		}
	}
	n, sg, c := pos[subtitle.VariantNormalized], pos[subtitle.VariantSegmented], pos[subtitle.VariantCorrected]
	if len(latest) > 0 || from != "" {
		// 最新的 run 不是 done（skipped、還在 pending 的步驟——重跑停在 until 之前——或其他）：
		// 這個步驟在現行脈絡沒有產出，現行的產出是舊脈絡留下的。normalize 也一樣：
		// 從 transcript 重跑到 transcript 為止之後，normalized 是舊來源的字幕（Codex P1）。
		fromIdx := -1
		if from != "" {
			fromIdx = slices.Index(Stages, from)
		}
		stale := func(stage string) bool {
			if fromIdx >= 0 && slices.Index(Stages, stage) >= fromIdx {
				return true
			}
			run, ok := latest[stage]
			return ok && run.Status != store.StatusDone
		}
		if stale(StageCorrect) {
			c = -1
		}
		if stale(StageSegment) {
			sg = -1
		}
		if stale(StageNormalize) {
			n = -1
		}
		switch {
		case c >= 0:
			return arts[c], true
		case sg >= 0:
			return arts[sg], true
		case n >= 0:
			return arts[n], true
		}
		return store.Artifact{}, false
	}
	switch {
	case c >= 0 && c > sg && c > n:
		return arts[c], true
	case sg >= 0 && sg > n:
		return arts[sg], true
	case n >= 0:
		return arts[n], true
	}
	return store.Artifact{}, false
}

// translateInputFor 讀取影音現行的字幕產出與各步驟最新的 stage run，依 translateInput 選 translate 的輸入。
// from 是 Rerun 即將重設的起點步驟（空字串代表沒有）。
func translateInputFor(ctx context.Context, r *store.Repo, itemID, from string) (store.Artifact, bool, error) {
	arts, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return store.Artifact{}, false, err
	}
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return store.Artifact{}, false, err
	}
	art, ok := translateInput(arts, latestRuns(runs), from)
	return art, ok, nil
}

// stageContext 讀取影音現行的字幕產出，決定 StageContext；在呼叫端的交易（或 Read）中執行。
// 原文語言是 translate 輸入字幕的語言；沒有字幕時退回 items.original_lang。
// 中文標記沒有 script、也推不出地區時（例如 zh），用字幕文字的字元補上 Hans 或 Hant（S6-Q3）；判斷不出來就維持原樣。
// from 是 Rerun 即將重設的起點步驟（空字串代表沒有）：它與之後的步驟現行的產出不算在內。
func (p *Pipeline) stageContext(ctx context.Context, r *store.Repo, it store.Item, from string) (StageContext, error) {
	art, ok, err := translateInputFor(ctx, r, it.ID, from)
	if err != nil {
		return StageContext{}, err
	}
	lang := it.OriginalLang
	if ok && art.Lang != "" {
		lang = art.Lang
	}
	if ok && langtag.IsChinese(lang) && langtag.ChineseScript(lang) == "" {
		if tr, err := readTranscript(p.dataDir, art.Path, art.SHA256); err == nil {
			lang = withDetectedScript(lang, tr.Cues)
		}
		// 讀不到字幕時不在這裡失敗：維持原樣，執行時會再讀一次並回報錯誤。
	}
	return StageContext{SourceLang: lang}, nil
}

// withDetectedScript 在 lang 沒有 script 時依 cue 文字補上 Hans 或 Hant；判斷不出來時回傳原值。
func withDetectedScript(lang string, cues []subtitle.Cue) string {
	texts := make([]string, 0, len(cues))
	for _, c := range cues {
		texts = append(texts, c.Text)
	}
	script := zhconv.DetectScriptOf(texts)
	if script == "" {
		return lang
	}
	t := langtag.Parse(lang)
	t.Script = script
	return t.String()
}

// openccParams 是 opencc 步驟快取鍵的 params：會影響輸出的目標與轉換表，以及字典版本。
type openccParams struct {
	Targets []openccTarget `json:"targets"`
	Data    string         `json:"data"`
}

type openccTarget struct {
	Lang  string       `json:"lang"`
	Table zhconv.Table `json:"table"`
}
