package pipeline

import (
	"context"
	"slices"

	"github.com/cxyfer/notesteme/internal/langtag"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// StageContext 是解析步驟執行方式時需要的影音資訊；由呼叫端從資料庫取得，ResolveExecutor 仍是純函式。
type StageContext struct {
	// SourceLang 是 translate 輸入字幕的語言（見 translateInput），不是 items.original_lang：
	// NRM-R7、OUT 的場景中兩者可能不同。還沒有字幕時退回 items.original_lang；中文沒有 script 時已用字元補上。
	// 空字串代表不知道原文語言：每個目標都需要翻譯（TargetTranslate）。
	SourceLang string
	// ManualCaptions 為 true 時，影音採用的字幕來源是人工字幕（kind manual）：segment 預設略過（SEG-R1）。
	ManualCaptions bool
}

// StageResolution 是步驟在一份生效設定下的執行方式。
// （和網址辨識的 Resolution 不同；契約原本叫 Resolution，與 resolve.go 的型別同名，所以加上 Stage 前綴。）
type StageResolution struct {
	Stage    string
	Executor string // ExecutorLocal、API、Agent、Off、OpenCC
	Provider string
	Model    string
	Prompt   string
	// Skip 為 true 時，這個步驟不建任務、不呼叫任何東西，前進到它時直接標成 skipped（原因是 Reason）。
	Skip   bool
	Reason string // Skip 時為 Skip* 常數其中之一
	// Wanted 只在 translate 有值：這份設定要求產生譯文的目標語言（正規化後，不含與原文相同的）。
	// translate 被略過時，不在其中的現行 translated 產出要撤下（applyChain）。
	Wanted []string
	// Chunks 是重跑時只重新送出的 chunk（POST /rerun 的 chunks，LLM-R8）；只用於重跑的起點步驟。
	Chunks []int
}

// ResolveExecutor 依步驟與生效設定決定執行方式。純函式：不讀 DB、不讀時鐘。
//   - metadata、transcript、normalize：local。
//   - segment、correct：executor 為 off 時略過（off）；api（預設）而 provider 或 model 是空的時略過（not_configured，
//     Executor 仍記為 api）；segment 的來源是人工字幕、而 stages.segment.on_manual 不是 run 時略過（manual_captions，SEG-R1）；
//     其他不略過。
//   - translate：依序判斷（TRN-R8、R10）：off 略過（off，優先於自動轉換）；每個目標都與原文相同（或只差字形而沒有轉換表）時略過
//     （same_language）；其餘目標全是 convert 時一律用 opencc，不論設定的是 api 或 agent，也不需要 provider；
//     明確設定 opencc 時用 opencc；其他情況比照 segment 與 correct。
//   - publish：沒有 outputs 時略過（no_outputs），有 outputs 時不略過。
func ResolveExecutor(eff Effective, stage string, sc StageContext) (StageResolution, error) {
	switch {
	case stage == StageMetadata || stage == StageTranscript || stage == StageNormalize:
		return StageResolution{Stage: stage, Executor: ExecutorLocal}, nil
	case IsLLMStage(stage):
		cfg, err := eff.Stage(stage)
		if err != nil {
			return StageResolution{}, err
		}
		if !slices.Contains(StageExecutors(stage), cfg.Executor) {
			return StageResolution{}, invalidf("stages.%s.executor 不支援 %q。", stage, cfg.Executor)
		}
		res := StageResolution{Stage: stage, Executor: cfg.Executor, Provider: cfg.Provider, Model: cfg.Model, Prompt: cfg.Prompt}
		var plans []TargetPlan
		if stage == StageTranslate {
			lang, err := eff.Language()
			if err != nil {
				return StageResolution{}, err
			}
			plans = PlanTargets(sc.SourceLang, lang)
			for _, t := range plans {
				if t.Kind != TargetSame {
					res.Wanted = append(res.Wanted, t.Lang)
				}
			}
		}
		switch {
		case cfg.Executor == ExecutorOff:
			res.Skip, res.Reason = true, SkipOff
		case stage == StageTranslate:
			switch {
			case len(res.Wanted) == 0:
				res.Skip, res.Reason = true, SkipSameLanguage
			case allConvert(plans):
				// 中文字形之間的轉換是確定性的本機處理：不需要 provider，也不呼叫 LLM（TRN-R8）。
				res.Executor, res.Provider, res.Model, res.Prompt = ExecutorOpenCC, "", "", ""
			case cfg.Executor == ExecutorAPI && (cfg.Provider == "" || cfg.Model == ""):
				res.Skip, res.Reason = true, SkipNotConfigured
			}
		case cfg.Executor == ExecutorAPI && (cfg.Provider == "" || cfg.Model == ""):
			res.Skip, res.Reason = true, SkipNotConfigured
		case stage == StageSegment && sc.ManualCaptions && eff.segmentOnManual() != OnManualRun:
			res.Skip, res.Reason = true, SkipManualCaptions
		}
		return res, nil
	case stage == StagePublish:
		outs, err := eff.Outputs()
		if err != nil {
			return StageResolution{}, err
		}
		res := StageResolution{Stage: stage, Executor: ExecutorLocal}
		if len(outs) == 0 {
			res.Skip, res.Reason = true, SkipNoOutputs
		}
		return res, nil
	}
	return StageResolution{}, invalidf("不認識的步驟 %q。", stage)
}

// planChain 從 from 開始依序解析，直到第一個不略過的步驟（含）或 until（含，空字串代表最後一個步驟）為止。
// 回傳的最後一個元素若 Skip 為 false，就是要建立任務的步驟；全部都略過時代表做到 until 或最後。
func planChain(eff Effective, from, until string, sc StageContext) ([]StageResolution, error) {
	i := slices.Index(Stages, from)
	if i < 0 {
		return nil, invalidf("不認識的步驟 %q。", from)
	}
	end := len(Stages) - 1
	if until != "" {
		end = slices.Index(Stages, until)
	}
	var chain []StageResolution
	for _, stage := range Stages[i:max(i, end+1)] {
		res, err := ResolveExecutor(eff, stage, sc)
		if err != nil {
			return nil, err
		}
		chain = append(chain, res)
		if !res.Skip {
			break
		}
	}
	return chain, nil
}

// stageImplemented 回報 (stage, executor) 有沒有實作。目前是三個 M1 步驟的 local、translate 的 opencc，以及 LLM 步驟的 agent
// （只建立任務並等 Agent 領取，AGT-R1；領取、租約與逾時屬於之後的切片）；
// 由 Handle 的分派與 Rerun 的 501 判斷共用，之後的切片完成時在這裡登記。
func stageImplemented(stage, executor string) bool {
	switch {
	case stage == StageMetadata || stage == StageTranscript || stage == StageNormalize || stage == StagePublish:
		return executor == ExecutorLocal
	case stage == StageTranslate && executor == ExecutorOpenCC:
		return true
	case IsLLMStage(stage):
		// api：有登記在 llmStages 的步驟（correct、segment）。
		return executor == ExecutorAgent || usesLLMEngine(stage, executor)
	}
	return false
}

// skippedState 是前進時略過的步驟的狀態：原因寫在 block_reason，沒有 message 與 actions。
// 不帶 message 是因為 pickAttentionStage 找第一個「不是 done 而且有 message」的步驟，
// skipped 一帶 message 就會蓋掉 done 步驟上的提醒（SRC-R15）。
func skippedState(reason string) store.StageState {
	return store.StageState{Status: store.StatusSkipped, BlockReason: reason}
}

// applyChain 依 chain 處理剛建立或剛前進到的步驟：記錄解析後的 executor 與 impl_version，
// 略過的寫成 skipped（原因在 block_reason），要執行的替它建立任務。runs 是各步驟要寫入的 stage run。
// 回傳是否建立了任務（呼叫端要喚醒 worker）。在呼叫端的交易內執行。
func (p *Pipeline) applyChain(ctx context.Context, r *store.Repo, itemID string, chain []StageResolution, runs map[string]store.StageRun, priority int, transcriptASR bool) (created bool, err error) {
	for _, res := range chain {
		run, ok := runs[res.Stage]
		if !ok {
			// 舊影音（只有 M1 的 3 個步驟）沒有這個步驟的記錄，沒有東西可以寫。
			break
		}
		if err := r.SetStageResult(ctx, run.ID, store.StageResult{Executor: res.Executor, ImplVersion: p.implVersion(res.Stage)}); err != nil {
			return created, err
		}
		if res.Skip {
			if err := r.SetStageState(ctx, run.ID, skippedState(res.Reason)); err != nil {
				return created, err
			}
			if res.Stage == StageTranslate {
				// 略過（沒有目標需要譯文、沒有設定 LLM、或 off）：為已不再要求的目標產生的 translated
				// 不再是現行版本，否則預設字幕選擇仍會回傳舊譯文（Codex P2）。保留歷史版本。
				if err := withdrawUnwanted(ctx, r, itemID, res.Wanted); err != nil {
					return created, err
				}
			}
			continue
		}
		run.Executor = res.Executor
		if _, err := insertStageTaskChunks(ctx, r, itemID, run, priority, transcriptASR, res.Chunks); err != nil {
			return created, err
		}
		if res.Executor == ExecutorAgent {
			// agent：建立任務後步驟等 Agent 領取（AGT-R1）。內建 worker 不領這種任務，所以不需要喚醒它。
			if err := r.SetStageState(ctx, run.ID, store.StageState{Status: store.StatusAwaitingAgent}); err != nil {
				return created, err
			}
			continue
		}
		created = true
	}
	return created, nil
}

// withdrawUnwanted 撤下現行的 translated 產出中，語言（正規化後）不在 wanted 裡的。
func withdrawUnwanted(ctx context.Context, r *store.Repo, itemID string, wanted []string) error {
	cur, err := r.CurrentArtifacts(ctx, itemID, "transcript")
	if err != nil {
		return err
	}
	var langs []string
	for _, a := range cur {
		if a.Variant == string(subtitle.VariantTranslated) && !slices.Contains(wanted, langtag.Parse(a.Lang).String()) && !slices.Contains(langs, a.Lang) {
			langs = append(langs, a.Lang)
		}
	}
	if len(langs) == 0 {
		return nil // 空的 langs 在 WithdrawArtifacts 代表全部語言
	}
	return r.WithdrawArtifacts(ctx, itemID, "transcript", string(subtitle.VariantTranslated), langs)
}
