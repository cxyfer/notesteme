package pipeline

import (
	"context"
	"math"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/llm"
)

// 新增前還沒有字幕可以量，LLM 費用（ING-R8 的 llm_usd）用片長推估字幕資料區塊的 token 數（ING 待決問題）：
// 每秒 estimateDataTokensPerSecond 個 token，約是每分鐘 240 個語音 token（偏快的語速）加上資料區塊的編號與格式。
// 刻意偏高：估價是上限的參考，實際金額以處理時的帳本為準。
const estimateDataTokensPerSecond = 6

// llmEstimate 是一部影音 LLM 費用的估算結果。
type llmEstimate struct {
	usd     cost.Micro
	unknown int // 無法估算的付費步驟數
}

// estimateLLM 估算一部影音從頭處理到最後一個步驟的 LLM 費用，方法與重跑的估算（estimateLLMStage）一致：
// 每個步驟依生效設定解析 executor（ResolveExecutor），只有 api 的 LLM 步驟計費；每個 chunk 依 COST-R5 估算
// （輸入＝prompt 加資料區塊，輸出＝資料區塊的 1.2 倍），單價依 COST-R2（覆寫優先，再來是內建計價表）。
// 片長不知道、找不到單價、或是 vision、Gemini 這類沒有估算方法的呼叫都算 unknown。不寫入任何資料、不呼叫外部服務。
func (p *Pipeline) estimateLLM(ctx context.Context, eff Effective, sc StageContext, title string, durationS *float64) (llmEstimate, error) {
	var out llmEstimate
	for _, stage := range Stages {
		res, err := ResolveExecutor(eff, stage, sc)
		if err != nil {
			return llmEstimate{}, err
		}
		if res.Skip || (gatedStage(stage) && !stageImplemented(stage, res.Executor) && !geminiNoteRes(eff, stage, res.Executor)) {
			continue
		}
		switch {
		case res.Executor != ExecutorAPI:
			// local、opencc、rules 不花錢；agent 由 Agent 自己的帳號計費（COST-R3）。
		case usesLLMEngine(stage, res.Executor):
			n := 1
			if stage == StageTranslate {
				n = llmTargets(eff, sc)
			}
			if n == 0 {
				continue
			}
			e, err := p.estimateLLMStageFromDuration(ctx, eff, stage, title, durationS)
			if err != nil {
				return llmEstimate{}, err
			}
			out.usd += e.usd * cost.Micro(n)
			out.unknown += e.unknown * n
		default:
			out.unknown++
		}
	}
	return out, nil
}

// llmTargets 是 translate 要呼叫 LLM 的目標數（翻譯與在地化；OpenCC 轉換、相同語言、平台字幕不算）。
func llmTargets(eff Effective, sc StageContext) int {
	lang, err := eff.Language()
	if err != nil {
		return 1
	}
	n := 0
	for _, t := range PlanTargets(sc.SourceLang, lang) {
		if t.Kind == TargetTranslate || t.Kind == TargetLocalize {
			n++
		}
	}
	return n
}

// estimateLLMStageFromDuration 估算一個 LLM 步驟：資料區塊的 token 數用片長推估，依 llm.chunk.max_tokens 切成 chunk，
// 每個 chunk 的輸入再加上 prompt（與標題）的 token 數。
func (p *Pipeline) estimateLLMStageFromDuration(ctx context.Context, eff Effective, stage, title string, durationS *float64) (llmEstimate, error) {
	if p.cost == nil || durationS == nil || !(*durationS > 0) || math.IsInf(*durationS, 0) {
		return llmEstimate{unknown: 1}, nil
	}
	cfg, err := eff.Stage(stage)
	if err != nil {
		return llmEstimate{}, err
	}
	settings, err := eff.LLM()
	if err != nil {
		return llmEstimate{}, err
	}
	promptTokens := llm.EstimateText(title)
	if pr, err := p.stagePrompt(ctx, cfg); err == nil {
		promptTokens += llm.EstimateText(pr.Body)
	} else if ctx.Err() != nil {
		return llmEstimate{}, ctx.Err()
	}
	data := int(math.Ceil(*durationS * estimateDataTokensPerSecond))
	chunks := max(1, int(math.Ceil(float64(data)/float64(max(settings.Chunk.MaxTokens, 1)))))
	call := cost.Call{
		Stage: stage, Provider: cfg.Provider, Model: cfg.Model, CustomBase: p.customBase(cfg.Provider),
		InputTokens: data + chunks*promptTokens,
		Output:      (data*12 + 9) / 10, // COST-R5：資料區塊的 1.2 倍，無條件進位
	}
	est, priced, err := p.cost.Estimate(ctx, p.st.Read(), call)
	if err != nil {
		return llmEstimate{}, err
	}
	if !priced && !p.cost.Estimated(stage) {
		return llmEstimate{unknown: 1}, nil
	}
	if p.cost.Estimated(stage) {
		// 測試控制的估算是「每個任務」的金額（steps.md 4.19），每個 chunk 一個任務。
		est *= cost.Micro(chunks)
	}
	return llmEstimate{usd: est}, nil
}

// estimateContextFor 是估價時的步驟情境：還沒有字幕，原文語言與章節數用 Probe 的結果。
func estimateContextFor(platform, sourceLang string, manual bool, chapters int, audioOnly bool) StageContext {
	return StageContext{SourceLang: sourceLang, ManualCaptions: manual, Platform: platform, PlatformChapters: chapters, AudioOnly: audioOnly}
}
