package pipeline

import (
	"context"

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
		plans = append(plans, planTarget(sourceLang, target, strategy))
	}
	return plans
}

func planTarget(source, target string, strategy zhconv.Strategy) TargetPlan {
	p := TargetPlan{Lang: target, Kind: TargetTranslate}
	switch {
	case langtag.Equal(source, target):
		p.Kind = TargetSame
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
func translateInput(arts []store.Artifact) (store.Artifact, bool) {
	for _, v := range []subtitle.Variant{subtitle.VariantCorrected, subtitle.VariantSegmented, subtitle.VariantNormalized} {
		for i := len(arts) - 1; i >= 0; i-- {
			if arts[i].Kind == "transcript" && arts[i].Variant == string(v) {
				return arts[i], true
			}
		}
	}
	return store.Artifact{}, false
}

// stageContext 讀取影音現行的字幕產出，決定 StageContext；在呼叫端的交易（或 Read）中執行。
// 原文語言是 translate 輸入字幕的語言；沒有字幕時退回 items.original_lang。
// 中文標記沒有 script、也推不出地區時（例如 zh），用字幕文字的字元補上 Hans 或 Hant（S6-Q3）；判斷不出來就維持原樣。
func (p *Pipeline) stageContext(ctx context.Context, r *store.Repo, it store.Item) (StageContext, error) {
	arts, err := r.CurrentArtifacts(ctx, it.ID, "transcript")
	if err != nil {
		return StageContext{}, err
	}
	art, ok := translateInput(arts)
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
