package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/cost"
	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/source"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
	"github.com/cxyfer/notesteme/internal/validate"
)

// 這個檔案是 prompt 的試跑（PRM-R6、PRM-R7 的輸出部分）：對指定影音的一段字幕，用指定的 prompt 版本與
// provider／model 跑一個 LLM 步驟，回傳輸出、驗證結果與預估花費。
//
// 試跑不經過 task queue 與 stage run，也不寫入任何東西：不建立產出、不改變步驟狀態、不寫 llm_chunks、
// 不加術語候選、不產生事件。它仍然受限流（Limiter）、netguard（provider client）、API key 取得與驗證重試規則約束。

const (
	// MaxPlaygroundCues 是一次試跑的字幕句數上限：避免一次花太多錢（超過回 422）。
	MaxPlaygroundCues = 200
	// playgroundTimeout 是一次試跑（含所有 chunk 與驗證重試）的時間上限。
	playgroundTimeout = 3 * time.Minute
	// maxPlaygrounds 是同時進行的試跑數上限；超過時請稍後再試（429）。
	maxPlaygrounds = 2
)

// playgroundSlots 讓同時進行的試跑不超過 maxPlaygrounds（跨請求共用）。
var playgroundSlots = make(chan struct{}, maxPlaygrounds)

// PlaygroundRequest 是一次試跑的請求。
type PlaygroundRequest struct {
	// Prompt 是 prompt 引用（"<id>"、"<id>@default"、"<id>@<N>"）；它的類型決定要跑哪個步驟。
	Prompt string
	// Item 是影音 ID 或短代號（例如 yt:abc123xxxxx）。
	Item string
	// FromMS、ToMS 是字幕範圍（毫秒）：開始時間在 [FromMS, ToMS) 的句子。
	FromMS, ToMS int64
	Provider     string
	Model        string
}

// PlaygroundCue 是試跑輸出的一句。
type PlaygroundCue struct {
	Index   int    `json:"index"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
	Text    string `json:"text"`
	// Original 是輸入的同一句（只有輸出與輸入一對一時才有，例如 correct）。
	Original string `json:"original,omitempty"`
}

// PlaygroundResult 是試跑的結果。
type PlaygroundResult struct {
	// Stage 是跑的步驟（由 prompt 的類型決定）。
	Stage string
	// Prompt 是實際解析到的版本。
	Prompt ResolvedPrompt
	// Input 是範圍內送出的句數。
	Inputs int
	Output []PlaygroundCue
	// Passed 為 false 代表至少一個 chunk 驗證重試用盡（Output 中那段是步驟的退回內容），Errors 列出原因。
	Passed bool
	Errors []string
	Usage  llm.Usage
	// EstimateUSD 是花費；內建計價表沒有這個 model 的價格時為 nil。
	EstimateUSD *float64
}

// stageOfPrompt 依 prompt 的類型找出要跑的步驟。
// 不是 LLM 步驟的類型（summary、chapters…）回 422；是 LLM 步驟但還不能試跑（沒有登記，或輸出不是一對一 cue 的 segment）回 501。
func stageOfPrompt(kind string) (string, error) {
	switch kind {
	case StageSegment, StageCorrect, StageTranslate:
		// 一對一的步驟（apply、fallback）才能用通用的流程試跑；輸出不是一對一 cue 的步驟（assemble，例如 segment）
		// 要自己合併與套用，試跑還沒有支援。
		if def, ok := llmStages[kind]; !ok || def.assemble != nil || def.apply == nil || def.fallback == nil {
			return "", &NotImplementedError{What: "試跑 " + kind + " 的提示詞"}
		}
		return kind, nil
	}
	return "", invalidFields([]validate.FieldError{{Path: "prompt", Message: fmt.Sprintf("類型 %s 的提示詞不能試跑（只有 segment、correct、translate）。", kind)}})
}

// itemByIDOrRef 用影音 ID 或短代號（yt:…、bili:…/p…、pod:…、local:…）找影音；垃圾桶中回 *InTrashError。
func (p *Pipeline) itemByIDOrRef(ctx context.Context, idOrRef string) (store.Item, error) {
	r := p.st.Read()
	var (
		it  store.Item
		err error
	)
	if platform, ext, part, ok := source.SplitRef(idOrRef); ok {
		it, err = r.ItemByKey(ctx, platform, ext, part)
	} else {
		it, err = r.ItemByID(ctx, idOrRef)
	}
	if err != nil {
		return store.Item{}, err
	}
	if it.DeletedAt != nil {
		return store.Item{}, &InTrashError{ItemID: it.ID}
	}
	return it, nil
}

// playgroundEffective 是試跑用的設定：影音最近一次設定快照（詞彙表、llm 區段、步驟專屬欄位如 min_similarity），
// 沒有快照時用內建預設。provider、model 與 prompt 由請求決定，不看快照。
func (p *Pipeline) playgroundEffective(ctx context.Context, itemID string) (Effective, error) {
	r := p.st.Read()
	runs, err := r.StageRunsByItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	for i := len(runs) - 1; i >= 0; i-- {
		raw, err := r.Snapshot(ctx, runs[i].ConfigSnapshotID)
		if err != nil {
			continue
		}
		if eff, err := ParseEffective(raw); err == nil {
			return eff, nil
		}
	}
	return BuildEffective()
}

// Playground 執行一次試跑。錯誤：*InvalidError（422：請求不正確、沒有字幕、範圍沒有句子或超過上限）、
// *InTrashError、store.ErrNotFound、ErrPromptNotFound 等（404）、*NotImplementedError、
// *errclass.Error（缺 API key 是 needs_action、限流是 wait、provider 失敗是 retryable 等，API 層對應狀態碼）。
func (p *Pipeline) Playground(ctx context.Context, req PlaygroundRequest) (PlaygroundResult, error) {
	var fields []validate.FieldError
	if req.Provider == "" {
		fields = append(fields, validate.FieldError{Path: "provider", Message: "provider 是必要的。"})
	}
	if req.Model == "" {
		fields = append(fields, validate.FieldError{Path: "model", Message: "model 是必要的。"})
	}
	if req.Prompt == "" {
		fields = append(fields, validate.FieldError{Path: "prompt", Message: "prompt 是必要的。"})
	}
	if req.Item == "" {
		fields = append(fields, validate.FieldError{Path: "item", Message: "item 是必要的。"})
	}
	if req.ToMS <= req.FromMS {
		fields = append(fields, validate.FieldError{Path: "to", Message: "to 必須在 from 之後。"})
	}
	if len(fields) > 0 {
		return PlaygroundResult{}, invalidFields(fields)
	}
	if p.llm == nil || p.prompts == nil {
		return PlaygroundResult{}, errors.New("pipeline：沒有設定 LLM provider 或 prompt 庫")
	}
	prov, ok := p.llm.Provider(req.Provider)
	if !ok {
		return PlaygroundResult{}, invalidFields([]validate.FieldError{{Path: "provider", Message: fmt.Sprintf("找不到 LLM provider「%s」。", req.Provider)}})
	}
	rp, err := p.prompts.ResolvePrompt(ctx, req.Prompt)
	if err != nil {
		return PlaygroundResult{}, err
	}
	stage, err := stageOfPrompt(rp.Kind)
	if err != nil {
		return PlaygroundResult{}, err
	}
	it, err := p.itemByIDOrRef(ctx, req.Item)
	if err != nil {
		return PlaygroundResult{}, err
	}
	eff, err := p.playgroundEffective(ctx, it.ID)
	if err != nil {
		return PlaygroundResult{}, err
	}

	def := llmStages[stage]
	settings, err := eff.LLM()
	if err != nil {
		return PlaygroundResult{}, err
	}
	extras, err := stageExtras(eff, stage)
	if err != nil {
		return PlaygroundResult{}, err
	}
	spec := def.spec(extras)

	// 輸入：影音現行的上游字幕（COR-R8 的順序，從這個步驟之前找），取範圍內的句子。
	r := p.st.Read()
	art, found, err := translateInputFor(ctx, r, it.ID, stage)
	if err != nil {
		return PlaygroundResult{}, err
	}
	if !found {
		return PlaygroundResult{}, invalidf("這部影音還沒有可以試跑的字幕，請先完成前面的步驟。")
	}
	tr, err := p.readTranscriptArtifact(ctx, art)
	if err != nil {
		return PlaygroundResult{}, err
	}
	var picked []subtitle.Cue
	for _, c := range tr.Cues {
		if c.StartMS >= req.FromMS && c.StartMS < req.ToMS {
			picked = append(picked, c)
		}
	}
	switch {
	case len(picked) == 0:
		return PlaygroundResult{}, invalidFields([]validate.FieldError{{Path: "from", Message: "這個範圍內沒有字幕。"}})
	case len(picked) > MaxPlaygroundCues:
		return PlaygroundResult{}, invalidFields([]validate.FieldError{{Path: "to", Message: fmt.Sprintf("範圍內有 %d 句，試跑最多 %d 句，請縮小範圍。", len(picked), MaxPlaygroundCues)}})
	}
	rangeTr := tr
	rangeTr.Cues = picked

	cues := make([]llm.Cue, len(picked))
	planCues := make([]llm.PlanCue, len(picked))
	for k, c := range picked {
		cues[k] = llm.Cue{I: k + 1, Text: c.Text}
		planCues[k] = llm.PlanCue{Start: time.Duration(c.StartMS) * time.Millisecond, Text: c.Text}
	}

	// 影片標題、說明等是不可信的資料：和正式執行一樣只放在 <video_metadata>（JSON 跳脫），不展開進系統指示。
	var md itemMetadata
	if len(it.Metadata) > 0 {
		_ = json.Unmarshal(it.Metadata, &md)
	}
	meta := llm.Metadata{Title: it.Title, Channel: md.Channel, Description: md.Description}
	var chapterStarts []time.Duration
	for _, c := range md.Chapters {
		start := time.Duration(c.Start * float64(time.Second))
		chapterStarts = append(chapterStarts, start)
		meta.Chapters = append(meta.Chapters, llm.ChapterInfo{Start: clockText(start), Title: c.Title})
	}
	slices.Sort(chapterStarts)
	chunks := llm.PlanChunks(planCues, chapterStarts, settings.Chunk)

	srcLang := tr.Lang
	if srcLang == "" {
		srcLang = it.OriginalLang
	}
	terms, err := p.glossaryTerms(ctx, it, eff)
	if err != nil {
		return PlaygroundResult{}, err
	}
	meta.Glossary = glossaryForSource(terms, srcLang, picked)
	fixed, err := llm.BuildFixed(spec, rp.Body, llm.TemplateVars{SourceLang: srcLang, Mixed: it.LangMixed}, meta)
	if err != nil {
		return PlaygroundResult{}, invalidFields([]validate.FieldError{{Path: "prompt", Message: "prompt 無法渲染：" + err.Error()}})
	}

	client, err := p.llm.Client(ctx, req.Provider)
	if err != nil {
		return PlaygroundResult{}, err
	}

	// 同時進行的試跑有上限；等不到名額就請 client 稍後再試。
	select {
	case playgroundSlots <- struct{}{}:
		defer func() { <-playgroundSlots }()
	default:
		return PlaygroundResult{}, &errclass.Error{
			Class: errclass.ClassWait, Source: "playground", Detail: "同時進行的試跑太多", RetryAfter: 2 * time.Second,
			Message: "同時進行的試跑太多，請稍後再試。",
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, playgroundTimeout)
	defer cancel()

	res := PlaygroundResult{Stage: stage, Prompt: rp, Inputs: len(picked), Passed: true, Errors: []string{}}
	outputs := make([][]llm.Cue, len(chunks))
	var estIn int
	for k, c := range chunks {
		in := llm.ChunkInput{Chunk: c, Chunks: len(chunks), Cues: cues[c.From-1 : c.To], Context: []llm.Cue{}}
		if c.HasContext() {
			in.Context = cues[c.ContextFrom-1 : c.ContextTo]
		}
		out, err := llm.RunChunk(runCtx, llm.RunOptions{
			Client: client, Spec: spec, Model: req.Model, Fixed: fixed, Input: in, MaxRetries: settings.MaxValidationRetries,
			BeforeRequest: func(_ context.Context, attempt int, r llm.Request) error {
				tokens := llm.RequestTokens(r)
				estIn += tokens
				if attempt > 0 {
					// 驗證重試立刻送出（LLM-R5），額度可以透支。
					p.llm.Limiter.Charge(prov.ID, prov.Limits, tokens)
					return nil
				}
				if wait := p.llm.Limiter.Reserve(prov.ID, prov.Limits, tokens); wait > 0 {
					// 同步請求不替 client 等：回報要等多久（LLM-R7）。
					return &errclass.Error{
						Class: errclass.ClassWait, Source: "llm:" + prov.ID, Detail: "速率限制", RetryAfter: wait,
						Message: fmt.Sprintf("LLM provider %s 的速率限制，請稍後再試。", prov.ID),
					}
				}
				return nil
			},
		})
		res.Usage.Add(out.Usage)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
				return PlaygroundResult{}, &errclass.Error{
					Class: errclass.ClassRetryable, Source: "playground", Detail: "試跑逾時", Message: "試跑逾時，請縮小範圍後再試。",
				}
			}
			return PlaygroundResult{}, err
		}
		if out.Output == nil {
			// 驗證重試用盡：這段用步驟的退回內容，並回報原因（不寫入任何東西）。
			res.Passed = false
			msg := fmt.Sprintf("第 %d 段驗證失敗：%s", c.Index, out.Fallback.Reason)
			if out.Fallback.Message != "" {
				msg = fmt.Sprintf("第 %d 段驗證失敗（%s）：%s", c.Index, out.Fallback.Reason, compactOneLine(out.Fallback.Message))
			}
			res.Errors = append(res.Errors, msg)
			outputs[k] = def.fallback(in)
			continue
		}
		if outputs[k], err = llm.DecodeCues(out.Output); err != nil {
			return PlaygroundResult{}, fmt.Errorf("解析模型輸出：%w", err)
		}
	}

	merged, err := llm.MergeCues(chunks, outputs)
	if err != nil {
		return PlaygroundResult{}, fmt.Errorf("合併 chunk 的輸出：%w", err)
	}
	applied, err := def.apply(rangeTr, merged)
	if err != nil {
		res.Passed = false
		res.Errors = append(res.Errors, "套用輸出失敗："+err.Error())
	} else {
		oneToOne := len(applied.Cues) == len(picked)
		for k, c := range applied.Cues {
			pc := PlaygroundCue{Index: k + 1, StartMS: c.StartMS, EndMS: c.EndMS, Text: c.Text}
			if oneToOne {
				pc.Original = picked[k].Text
			}
			res.Output = append(res.Output, pc)
		}
	}

	// 花費：以 provider 回報的 token 用量為準；沒有用量時用 L2 的估算（輸入）與輸入為上界（輸出）。
	in, outTok := res.Usage.InputTokens, res.Usage.OutputTokens
	if in == 0 && outTok == 0 {
		in, outTok = estIn, estIn
	}
	if usd, ok := cost.EstimateLLM(req.Provider, req.Model, in, outTok); ok {
		res.EstimateUSD = &usd
	}
	if res.Output == nil {
		res.Output = []PlaygroundCue{}
	}
	return res, nil
}

// compactOneLine 把訊息壓成單行，給錯誤清單用。
func compactOneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
