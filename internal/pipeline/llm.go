package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/llm"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// LLM 步驟走 api executor 時，一個 stage run 由三種任務完成（ADR-0004：chunk 即任務）：
//
//   - plan（協調）：讀取輸入、切 chunk、組固定前綴，寫入 llm_chunks；步驟層的快取命中時直接完成（PIPE-R5），
//     chunk 層的快取命中時重用（LLM-R8）。其餘每個 chunk 建立一個任務（llm 資源類別，依 chunk 順序建立）。
//   - chunk：送出一個 chunk，驗證失敗時立刻附上錯誤重試（LLM-R4、R5），寫入 chunk 的結果；
//     最後一個 chunk 在同一個寫入交易中建立 assemble 任務。
//   - assemble：依 chunk 順序合併輸出（重疊區以前一個 chunk 為準），寫出產出與來源紀錄，步驟完成並前進。
//
// plan 與 assemble 是 local 資源類別，不佔 llm 的並行數（AGT-R2 的剩餘並行數只算 chunk）。
const (
	llmPhasePlan     = "plan"
	llmPhaseChunk    = "chunk"
	llmPhaseAssemble = "assemble"
)

// llmPayload 是 LLM 步驟任務的 payload。
type llmPayload struct {
	ItemID string `json:"item_id"`
	Phase  string `json:"phase,omitempty"` // 空字串代表 plan（resume 重建的任務沒有 phase）
	// Chunks 是這次要強制重跑的 chunk（POST /rerun 的 chunks，LLM-R8）：繞過步驟層與 chunk 層的快取。
	Chunks []int `json:"chunks,omitempty"`
}

// llmStage 是一個 LLM 步驟在 pipeline 這一側的部分：Spec（schema 與驗證）、產出的變體、退回與套用。
// 之後的切片（segment、translate、correct 的 COR 規則）在 llmStages 登記自己的步驟。
type llmStage struct {
	// spec 依 stages.<stage> 的步驟專屬欄位（例如 correct 的 min_similarity，stageExtras）建立 Spec；extras 可以是 nil。
	spec    func(extras map[string]any) *llm.Spec
	variant subtitle.Variant
	// fallback 回傳驗證重試用盡時這個 chunk 的輸出（LLM-R5 的步驟退回規則）。
	fallback func(in llm.ChunkInput) []llm.Cue
	// apply 把合併後的輸出套用到輸入字幕，產生這個步驟的產出（時間沿用輸入）。
	apply func(in subtitle.Transcript, merged []llm.Cue) (subtitle.Transcript, error)
	// notes 回傳步驟完成時要附上的訊息與修正動作（例如 correct 有 chunk 因相似度退回，COR-R3）；沒有時為 nil。
	notes func(fallbacks []fallbackRecord) (message string, actions []string)
	// terms 取出已通過驗證的 chunk 輸出中的術語候選（COR-R6）；nil 代表這個步驟不收集術語候選。
	terms func(out json.RawMessage) ([]string, error)
}

// usesLLMEngine 回報步驟的任務是否走 LLM 引擎（plan、chunk、assemble 三種任務）：
// 只有 api executor、且登記在 llmStages 的步驟才會。
func usesLLMEngine(stage, executor string) bool {
	_, ok := llmStages[stage]
	return ok && executor == ExecutorAPI
}

var llmStages = map[string]llmStage{
	StageCorrect: {
		spec: func(extras map[string]any) *llm.Spec {
			return llm.CorrectSpecWith(extraFloat(extras, "min_similarity", llm.DefaultMinSimilarity))
		},
		variant: subtitle.VariantCorrected,
		// COR-R3：重試用盡就退回這段的原文。
		fallback: func(in llm.ChunkInput) []llm.Cue { return slices.Clone(in.Cues) },
		apply:    replaceCueTexts,
		notes:    correctNotes,
		terms:    llm.DecodeTermCandidates,
	},
}

// 登記時檢查每個步驟的 Spec（strict schema、步驟名稱一致）：不符是程式錯誤，啟動時就失敗，不要等到送出請求才被 provider 拒絕。
func init() {
	if err := checkLLMStages(llmStages); err != nil {
		panic(err)
	}
}

func checkLLMStages(stages map[string]llmStage) error {
	for stage, d := range stages {
		spec := d.spec(nil)
		if err := spec.Ready(); err != nil {
			return err
		}
		if spec.Stage != stage {
			return fmt.Errorf("llmStages[%s] 的 Spec 是 %s", stage, spec.Stage)
		}
	}
	return nil
}

// replaceCueTexts 一對一套用：第 i 句的文字換成輸出的文字，時間與其他欄位不變（COR-R1）。
func replaceCueTexts(in subtitle.Transcript, merged []llm.Cue) (subtitle.Transcript, error) {
	if len(merged) != len(in.Cues) {
		return subtitle.Transcript{}, fmt.Errorf("合併後有 %d 句，輸入有 %d 句", len(merged), len(in.Cues))
	}
	out := in
	out.Cues = slices.Clone(in.Cues)
	for k, c := range merged {
		if c.I != k+1 {
			return subtitle.Transcript{}, fmt.Errorf("合併後第 %d 句的句號是 %d", k+1, c.I)
		}
		out.Cues[k].Text = c.Text
	}
	return out, nil
}

// extraFloat 讀取步驟專屬欄位中的數字；沒有或型別不符時用預設值。
func extraFloat(extras map[string]any, key string, def float64) float64 {
	if v, ok := extras[key].(float64); ok {
		return v
	}
	return def
}

// 動作文字（COR-R3）。
const (
	actionRecorrectChunk = "重新校正這段"
	actionOpenPrompt     = "開啟 Prompt"
)

// correctNotes 是 correct 完成時附上的訊息（COR-R3）：有 chunk 因為相似度低於門檻而退回原文時，
// 指出第一個這樣的 chunk 與它最低的相似度；動作是重新校正這段、開啟 Prompt。
func correctNotes(fallbacks []fallbackRecord) (string, []string) {
	var first *fallbackRecord
	n := 0
	for i := range fallbacks {
		if fallbacks[i].Reason == llm.ReasonSimilarity {
			if first == nil {
				first = &fallbacks[i]
			}
			n++
		}
	}
	if first == nil {
		return "", nil
	}
	sim, _ := first.Detail["similarity"].(float64)
	msg := fmt.Sprintf("第 %d 段的校正結果和原文差異過大（相似度 %.2f），已退回原文。", first.Chunk, sim)
	if n > 1 {
		msg += fmt.Sprintf("另有 %d 段也退回原文。", n-1)
	}
	return msg, []string{actionRecorrectChunk, actionOpenPrompt}
}

// LLMSettings 是 llm 區段中處理方案可以設定的部分（llm.chunk、llm.max_validation_retries）。
type LLMSettings struct {
	Chunk                llm.ChunkParams
	MaxValidationRetries int
}

// LLM 取出 llm 區段，沒有設定的欄位用預設值（LLM-R1、R5）。
func (e Effective) LLM() (LLMSettings, error) {
	s := LLMSettings{Chunk: llm.DefaultChunkParams(), MaxValidationRetries: llm.DefaultMaxValidationRetries}
	raw, ok := e["llm"]
	if !ok || raw == nil {
		return s, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return s, err
	}
	var v struct {
		Chunk *struct {
			MaxTokens   *int `json:"max_tokens"`
			OverlapCues *int `json:"overlap_cues"`
			ContextCues *int `json:"context_cues"`
		} `json:"chunk"`
		MaxValidationRetries *int `json:"max_validation_retries"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return s, invalidf("llm 設定不正確：%v", err)
	}
	if c := v.Chunk; c != nil {
		if c.MaxTokens != nil {
			s.Chunk.MaxTokens = *c.MaxTokens
		}
		if c.OverlapCues != nil {
			s.Chunk.OverlapCues = *c.OverlapCues
		}
		if c.ContextCues != nil {
			s.Chunk.ContextCues = *c.ContextCues
		}
	}
	if v.MaxValidationRetries != nil {
		s.MaxValidationRetries = *v.MaxValidationRetries
	}
	return s, nil
}

// Glossary 取出處理方案的 glossary（字串陣列）；沒有時回傳 nil。頻道與影片的詞彙表屬於 COR 切片（COR-R4）。
func (e Effective) Glossary() []string {
	list, _ := e["glossary"].([]any)
	var out []string
	for _, v := range list {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// llmJob 是一個 LLM stage run 由設定快照與輸入字幕決定的全部內容；plan、chunk、assemble 都重新計算它，結果相同。
type llmJob struct {
	stage    string
	def      llmStage
	spec     *llm.Spec
	cfg      StageConfig
	settings LLMSettings
	input    store.Artifact
	tr       subtitle.Transcript
	cues     []llm.Cue
	chunks   []llm.Chunk
	fixed    llm.Fixed
	// glossary 是送給模型的詞彙表（已轉成原文字形）；glossarySet 含轉換前後的寫法，術語候選用它排除已在詞彙表中的詞。
	glossary    []string
	glossarySet map[string]bool
	prompt      ResolvedPrompt
	// paramsSHA 是會影響輸出的參數（chunk 設定、詞彙表、步驟專屬欄位）的 sha256（LLM-R10）。
	paramsSHA string
	// fixedSHA 是固定前綴（系統指示與 <video_metadata>）的 sha256，進快取鍵：標題或 prompt 改變時快取不命中。
	fixedSHA string
	stageKey string
}

// chunkPlan 是 llm_chunks.plan_json：chunk 的範圍與這次 stage run 的輸入產出。
type chunkPlan struct {
	llm.Chunk
	Chunks int    `json:"chunks"`
	Input  string `json:"input"` // 輸入產出的 ID
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// itemMetadata 是 items.metadata_json 中 LLM 步驟用到的欄位。
type itemMetadata struct {
	Description string `json:"description"`
	Channel     string `json:"channel"`
	Chapters    []struct {
		Title string  `json:"title"`
		Start float64 `json:"start"`
	} `json:"chapters"`
}

// prepareLLM 計算 llmJob。inputID 非空時用那份產出當輸入（chunk、assemble 沿用 plan 選定的輸入）；
// 空字串時依 translateInput 的規則選擇（correct：segmented > normalized）。
func (p *Pipeline) prepareLLM(ctx context.Context, it store.Item, stage string, eff Effective, inputID string) (*llmJob, error) {
	def, ok := llmStages[stage]
	if !ok {
		return nil, errPermanent("步驟 " + stage + " 沒有 LLM 的實作")
	}
	cfg, err := eff.Stage(stage)
	if err != nil {
		return nil, err
	}
	settings, err := eff.LLM()
	if err != nil {
		return nil, err
	}
	extras, err := stageExtras(eff, stage)
	if err != nil {
		return nil, err
	}
	job := &llmJob{stage: stage, def: def, spec: def.spec(extras), cfg: cfg, settings: settings}

	r := p.st.Read()
	if inputID != "" {
		job.input, err = r.Artifact(ctx, inputID)
		if errors.Is(err, store.ErrNotFound) {
			return nil, errPermanent("輸入的字幕產出已不存在")
		}
		if err != nil {
			return nil, err
		}
	} else {
		art, found, err := translateInputFor(ctx, r, it.ID, stage)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "沒有輸入字幕",
				Message: "找不到可以處理的字幕，請先完成前面的步驟。"}
		}
		job.input = art
	}
	job.tr, err = readTranscript(p.dataDir, job.input.Path, job.input.SHA256)
	if err != nil {
		if errors.Is(err, ErrCorruptArtifact) {
			return nil, errPermanent(err.Error())
		}
		return nil, err
	}
	planCues := make([]llm.PlanCue, len(job.tr.Cues))
	job.cues = make([]llm.Cue, len(job.tr.Cues))
	for k, c := range job.tr.Cues {
		job.cues[k] = llm.Cue{I: k + 1, Text: c.Text}
		planCues[k] = llm.PlanCue{Start: time.Duration(c.StartMS) * time.Millisecond, Text: c.Text}
	}

	var md itemMetadata
	if len(it.Metadata) > 0 {
		_ = json.Unmarshal(it.Metadata, &md) // 格式不對時當成沒有中繼資料
	}
	meta := llm.Metadata{Title: it.Title, Channel: md.Channel, Description: md.Description}
	var chapterStarts []time.Duration
	for _, c := range md.Chapters {
		start := time.Duration(c.Start * float64(time.Second))
		chapterStarts = append(chapterStarts, start)
		meta.Chapters = append(meta.Chapters, llm.ChapterInfo{Start: clockText(start), Title: c.Title})
	}
	slices.Sort(chapterStarts)
	job.chunks = llm.PlanChunks(planCues, chapterStarts, settings.Chunk)

	job.prompt, err = p.stagePrompt(ctx, cfg)
	if err != nil {
		return nil, err
	}
	srcLang := job.tr.Lang
	if srcLang == "" {
		srcLang = it.OriginalLang
	}
	// 詞彙表（COR-R4）：全域、頻道、處理方案與頻道覆寫合併，再轉成原文字形；放在 <video_metadata>（不可信資料）中。
	terms, err := p.glossaryTerms(ctx, it, eff)
	if err != nil {
		return nil, err
	}
	glossary := glossaryForSource(terms, srcLang, job.tr.Cues)
	job.glossary = glossary
	job.glossarySet = map[string]bool{}
	for _, t := range slices.Concat(terms, glossary) {
		job.glossarySet[strings.ToLower(t)] = true
	}
	meta.Glossary = glossary
	// COR-R5：原文語言標記為 mixed 時，步驟的 MixedRule 加進系統指示。
	job.fixed, err = llm.BuildFixed(job.spec, job.prompt.Body, llm.TemplateVars{SourceLang: srcLang, Mixed: it.LangMixed}, meta)
	if err != nil {
		return nil, errPermanent("prompt 無法渲染：" + err.Error())
	}
	fixedJSON, _ := json.Marshal(job.fixed)
	job.fixedSHA = sha256Hex(fixedJSON)

	paramsJSON, err := json.Marshal(map[string]any{"chunk": settings.Chunk, "glossary": glossary, "stage": extras})
	if err != nil {
		return nil, err
	}
	job.paramsSHA = sha256Hex(paramsJSON)
	job.stageKey = cacheKey{
		Item: it.ID, Stage: stage, Impl: p.implVersion(stage), Upstream: job.input.SHA256 + "+" + job.fixedSHA,
		Provider: cfg.Provider, Model: cfg.Model, Prompt: job.prompt.SHA256, Params: job.paramsSHA,
	}.String()
	return job, nil
}

// clockText 把時間寫成 HH:MM:SS。
func clockText(d time.Duration) string {
	s := int(d / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}

// stageExtras 回傳 stages.<stage> 中通用欄位以外的設定（例如 correct 的 min_similarity），它們會影響輸出。
func stageExtras(eff Effective, stage string) (map[string]any, error) {
	all, err := eff.stages()
	if err != nil || all == nil {
		return nil, err
	}
	entry, _ := all[stage].(map[string]any)
	out := map[string]any{}
	for k, v := range entry {
		if !slices.Contains(stageCommonFields, k) && k != "prompt_version" && k != "prompt_sha256" {
			out[k] = v
		}
	}
	return out, nil
}

// stagePrompt 讀出快照記下的 prompt 版本（D11）。快照沒有版本（解析失敗）時，在這裡以 permanent 失敗；
// 內容的 sha256 與快照不同時也失敗（版本建立後不能修改，PRM-R2）。
func (p *Pipeline) stagePrompt(ctx context.Context, cfg StageConfig) (ResolvedPrompt, error) {
	if p.prompts == nil {
		return ResolvedPrompt{}, errPermanent("沒有 prompt 庫")
	}
	if cfg.PromptVersion == 0 {
		return ResolvedPrompt{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "快照沒有 prompt 版本 " + cfg.Prompt,
			Message: fmt.Sprintf("找不到 prompt「%s」，請檢查處理方案的設定。", cfg.Prompt)}
	}
	id, _ := splitPromptRef(cfg.Prompt)
	rp, err := p.prompts.ResolvePrompt(ctx, fmt.Sprintf("%s@%d", id, cfg.PromptVersion))
	if err != nil {
		if errors.Is(err, ErrPromptNotFound) || errors.Is(err, ErrPromptVersionNotFound) {
			return ResolvedPrompt{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: err.Error(),
				Message: fmt.Sprintf("找不到 prompt「%s」第 %d 版，請檢查處理方案的設定。", id, cfg.PromptVersion)}
		}
		return ResolvedPrompt{}, err
	}
	if cfg.PromptSHA256 != "" && rp.SHA256 != cfg.PromptSHA256 {
		return ResolvedPrompt{}, errPermanent("prompt " + id + " 的內容與快照記錄的 sha256 不同")
	}
	return rp, nil
}

// splitPromptRef 拆開 "<id>"、"<id>@default"、"<id>@<N>" 的 id 與版本部分。
func splitPromptRef(ref string) (id, version string) {
	for i := len(ref) - 1; i >= 0; i-- {
		if ref[i] == '@' {
			return ref[:i], ref[i+1:]
		}
	}
	return ref, ""
}

// chunkInput 組出第 k 個 chunk（從 0 起算的索引）送給模型的資料。
func (j *llmJob) chunkInput(c llm.Chunk) llm.ChunkInput {
	in := llm.ChunkInput{Chunk: c, Chunks: len(j.chunks), Cues: j.cues[c.From-1 : c.To]}
	if c.HasContext() {
		in.Context = j.cues[c.ContextFrom-1 : c.ContextTo]
	} else {
		in.Context = []llm.Cue{}
	}
	return in
}

// chunkKey 是 chunk 層的快取鍵（LLM-R8）：步驟、實作版本、provider、model、固定前綴與這個 chunk 的資料區塊。
func (j *llmJob) chunkKey(impl int, c llm.Chunk) (string, error) {
	in := j.chunkInput(c)
	data, err := llm.DataBlock(c.Index, len(j.chunks), in.Context, in.Cues)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(map[string]any{
		"stage": j.stage, "impl_version": impl, "provider": j.cfg.Provider, "model": j.cfg.Model,
		"fixed": j.fixedSHA, "params": j.paramsSHA, "data": data,
	})
	if err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

// runLLM 執行 LLM 步驟（api executor）的一個任務，依 payload 的 phase 分派。
func (p *Pipeline) runLLM(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective) (result, error) {
	if p.llm == nil {
		return result{}, errPermanent("沒有設定 LLM provider")
	}
	var pl llmPayload
	if len(t.Payload) > 0 {
		if err := json.Unmarshal(t.Payload, &pl); err != nil {
			return result{}, errPermanent("任務的 payload 不正確：" + err.Error())
		}
	}
	switch {
	case t.ChunkIndex != nil:
		return p.llmChunk(ctx, t, run, it, eff)
	case pl.Phase == llmPhaseAssemble:
		return p.llmAssemble(ctx, run, it, eff)
	default:
		return p.llmPlan(ctx, t, run, it, eff, pl.Chunks)
	}
}

// llmPlan 是協調任務：切 chunk、查快取，建立 chunk 任務；全部命中快取時直接組出產出。
func (p *Pipeline) llmPlan(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective, force []int) (result, error) {
	job, err := p.prepareLLM(ctx, it, run.Stage, eff, "")
	if err != nil {
		return result{}, err
	}
	if len(job.chunks) == 0 {
		return result{}, &errclass.Error{Class: errclass.ClassPermanent, Source: "pipeline", Detail: "輸入字幕沒有任何句子",
			Message: "字幕是空的，沒有可以處理的內容。"}
	}
	stage := &store.StageResult{Executor: ExecutorAPI, CacheKey: job.stageKey, ImplVersion: p.implVersion(run.Stage)}
	r := p.st.Read()
	// 步驟層的快取（PIPE-R5）：相同輸入與設定的完整結果已經存在時直接重用。指定重跑的 chunk 時不用。
	if len(force) == 0 {
		if hit, err := r.DoneStageByCacheKey(ctx, it.ID, run.Stage, job.stageKey); err == nil && hit.ArtifactID != "" {
			stage.ArtifactID = hit.ArtifactID
			return result{task: taskDone, advance: true, stage: stage, state: store.StageState{Status: store.StatusDone}}, nil
		}
	}

	rows := make([]store.LLMChunk, 0, len(job.chunks))
	var pending []int
	for _, c := range job.chunks {
		key, err := job.chunkKey(p.implVersion(run.Stage), c)
		if err != nil {
			return result{}, err
		}
		plan, err := json.Marshal(chunkPlan{Chunk: c, Chunks: len(job.chunks), Input: job.input.ID})
		if err != nil {
			return result{}, err
		}
		row := store.LLMChunk{StageRunID: run.ID, Chunk: c.Index, ItemID: it.ID, Stage: run.Stage, CacheKey: key, Status: store.ChunkPending, Plan: plan}
		// chunk 層的快取（LLM-R8）：只重用通過驗證的結果；退回的 chunk 與指定重跑的 chunk 一律重新送出。
		if !slices.Contains(force, c.Index) {
			if hit, err := r.DoneLLMChunkByCacheKey(ctx, it.ID, run.Stage, key); err == nil {
				row.Status, row.Output, row.Reused = store.ChunkDone, hit.Output, true
			} else if !errors.Is(err, store.ErrNotFound) {
				return result{}, err
			}
		}
		if row.Status == store.ChunkPending {
			pending = append(pending, c.Index)
		}
		rows = append(rows, row)
	}
	insertRows := func(ctx context.Context, tx *store.Repo) error {
		for _, row := range rows {
			if err := tx.InsertLLMChunk(ctx, row); err != nil {
				return err
			}
		}
		return nil
	}
	if len(pending) == 0 {
		// 每個 chunk 都重用了之前的結果：在這個任務中直接組出產出。
		res, err := p.assembleLLM(ctx, it, job, rows, stage)
		if err != nil {
			return result{}, err
		}
		assembleTx := res.inTx
		res.inTx = func(ctx context.Context, tx *store.Repo) (bool, error) {
			if err := insertRows(ctx, tx); err != nil {
				return false, err
			}
			if assembleTx != nil {
				return assembleTx(ctx, tx)
			}
			return false, nil
		}
		return res, nil
	}
	return result{task: taskDone, keepState: true, inTx: func(ctx context.Context, tx *store.Repo) (bool, error) {
		if err := insertRows(ctx, tx); err != nil {
			return false, err
		}
		payload, err := json.Marshal(llmPayload{ItemID: it.ID, Phase: llmPhaseChunk})
		if err != nil {
			return false, err
		}
		// 依 chunk 順序建立：同一個交易中建立時間相同，領取依 rowid，請求的順序才會依 chunk 編號。
		for _, k := range pending {
			idx := k
			if _, err := tx.InsertTask(ctx, store.NewTask{
				StageRunID: run.ID, Kind: run.Stage, Executor: queue.ExecutorBuiltin, ResourceClass: queue.ResourceLLM,
				Priority: t.Priority, Payload: payload, ChunkIndex: &idx, MaxAttempts: queue.DefaultMaxAttempts,
			}); err != nil {
				return false, err
			}
		}
		return true, nil
	}}, nil
}

// llmChunk 送出一個 chunk（含驗證重試），寫入 chunk 的結果；最後一個完成的 chunk 建立 assemble 任務。
// chunkLeaseMargin 是 chunk 的時間上限與租約到期之間保留的時間：結果要在租約內寫回。
const chunkLeaseMargin = time.Minute

// chunkTimeout 是 chunk 任務送出請求（含驗證重試）的時間上限：租約剩下的時間減去 chunkLeaseMargin。
// 到期時 RunChunk 回 retryable；租約沒有續租，若讓它失效，任務會被重新領取而重複送出（重複計費）。
// 已經不夠時回傳負數（RunChunk 不送出請求）。
func (p *Pipeline) chunkTimeout(t store.Task) time.Duration {
	left := queue.DefaultLease
	if t.LeaseUntil != nil {
		left = t.LeaseUntil.Sub(p.clock.Now())
	}
	if d := left - chunkLeaseMargin; d > 0 {
		return d
	}
	return -1
}

func (p *Pipeline) llmChunk(ctx context.Context, t store.Task, run store.StageRun, it store.Item, eff Effective) (result, error) {
	r := p.st.Read()
	row, err := r.LLMChunk(ctx, run.ID, *t.ChunkIndex)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return result{}, errPermanent(fmt.Sprintf("找不到 chunk %d 的記錄", *t.ChunkIndex))
		}
		return result{}, err
	}
	if row.Status != store.ChunkPending {
		// 已經有結果（例如租約失效後重新領取）：不再送出，也不改變步驟狀態。
		return result{task: taskDone, keepState: true}, nil
	}
	var plan chunkPlan
	if err := json.Unmarshal(row.Plan, &plan); err != nil {
		return result{}, errPermanent("chunk 的記錄不正確：" + err.Error())
	}
	job, err := p.prepareLLM(ctx, it, run.Stage, eff, plan.Input)
	if err != nil {
		return result{}, err
	}
	if plan.To > len(job.cues) || plan.From < 1 || plan.Chunks != len(job.chunks) {
		return result{}, errPermanent(fmt.Sprintf("chunk %d 的範圍與輸入字幕不符", plan.Index))
	}
	in := job.chunkInput(plan.Chunk)

	client, err := p.llm.Client(ctx, job.cfg.Provider)
	if err != nil {
		return result{}, err
	}
	prov, _ := p.llm.Provider(job.cfg.Provider)
	limiter := p.llm.Limiter
	out, err := llm.RunChunk(ctx, llm.RunOptions{
		Client: client, Spec: job.spec, Model: job.cfg.Model, Fixed: job.fixed, Input: in,
		MaxRetries: job.settings.MaxValidationRetries, Timeout: p.chunkTimeout(t),
		BeforeRequest: func(_ context.Context, attempt int, req llm.Request) error {
			tokens := llm.RequestTokens(req)
			if attempt > 0 {
				// 驗證重試立刻送出（LLM-R5），額度可以透支，由之後的請求等待補回。
				limiter.Charge(prov.ID, prov.Limits, tokens)
				return nil
			}
			if wait := limiter.Reserve(prov.ID, prov.Limits, tokens); wait > 0 {
				// 超過 RPM／TPM：任務等待，不算錯誤（LLM-R7）。
				return &errclass.Error{
					Class: errclass.ClassWait, Source: "llm:" + prov.ID, Detail: "速率限制", RetryAfter: wait,
					Message: fmt.Sprintf("等待 LLM provider %s 的速率限制，稍後會繼續。", prov.ID),
				}
			}
			return nil
		},
	})
	if err != nil {
		return result{}, err
	}
	usage, err := json.Marshal(out.Usage)
	if err != nil {
		return result{}, err
	}
	status, reason, output, detail := store.ChunkDone, "", json.RawMessage(out.Output), json.RawMessage(nil)
	if out.Output == nil {
		status, reason, output = store.ChunkFallback, out.Fallback.Reason, nil
		if len(out.Fallback.Detail) > 0 {
			if detail, err = json.Marshal(out.Fallback.Detail); err != nil {
				return result{}, err
			}
		}
		p.log.Warn("chunk 驗證重試用盡，退回", "item", it.ID, "stage", run.Stage, "chunk", plan.Index, "reason", reason, "detail", out.Fallback.Message)
	}
	return result{task: taskDone, keepState: true, inTx: func(ctx context.Context, tx *store.Repo) (bool, error) {
		ok, err := tx.FinishLLMChunk(ctx, run.ID, plan.Index, status, reason, output, usage, detail)
		if err != nil || !ok {
			return false, err
		}
		cur, err := tx.StageRun(ctx, run.ID)
		if err != nil {
			return false, err
		}
		if stageStopped(cur.Status) {
			// 其他 chunk 已讓步驟停下（或步驟已結束）：只記下這個 chunk 的結果，之後的重跑可以重用。
			return false, nil
		}
		n, err := tx.CountPendingLLMChunks(ctx, run.ID)
		if err != nil || n > 0 {
			return false, err
		}
		payload, err := json.Marshal(llmPayload{ItemID: it.ID, Phase: llmPhaseAssemble})
		if err != nil {
			return false, err
		}
		_, err = tx.InsertTask(ctx, store.NewTask{
			StageRunID: run.ID, Kind: run.Stage, Executor: queue.ExecutorBuiltin, ResourceClass: queue.ResourceLocal,
			Priority: t.Priority, Payload: payload, MaxAttempts: queue.DefaultMaxAttempts,
		})
		return err == nil, err
	}}, nil
}

// llmAssemble 是 assemble 任務：所有 chunk 都有結果之後組出產出。
func (p *Pipeline) llmAssemble(ctx context.Context, run store.StageRun, it store.Item, eff Effective) (result, error) {
	r := p.st.Read()
	rows, err := r.LLMChunks(ctx, run.ID)
	if err != nil {
		return result{}, err
	}
	if len(rows) == 0 {
		return result{}, errPermanent("找不到這個步驟的 chunk 記錄")
	}
	var first chunkPlan
	if err := json.Unmarshal(rows[0].Plan, &first); err != nil {
		return result{}, errPermanent("chunk 的記錄不正確：" + err.Error())
	}
	job, err := p.prepareLLM(ctx, it, run.Stage, eff, first.Input)
	if err != nil {
		return result{}, err
	}
	stage := &store.StageResult{Executor: ExecutorAPI, CacheKey: job.stageKey, ImplVersion: p.implVersion(run.Stage)}
	return p.assembleLLM(ctx, it, job, rows, stage)
}

// chunkRecord 是來源紀錄 chunks[] 的一項（LLM-R10）：範圍、前文、token 用量，以及是否重用了之前的結果。
type chunkRecord struct {
	Chunk   int       `json:"chunk"`
	Cues    [2]int    `json:"cues"`
	Context *[2]int   `json:"context"`
	Usage   llm.Usage `json:"usage"`
	Reused  bool      `json:"reused,omitempty"`
}

// fallbackRecord 是來源紀錄 fallbacks[] 的一項（LLM-R5）。
type fallbackRecord struct {
	Chunk  int    `json:"chunk"`
	Reason string `json:"reason"`
	// Detail 是步驟記錄的補充資料（例如 correct 的 similarity）；沒有時省略。
	Detail map[string]any `json:"detail,omitempty"`
}

// assembleLLM 依 chunk 順序合併輸出、套用到輸入字幕、寫出產出（LLM-R1、R5、R10）。有退回的 chunk 時不寫入步驟層的快取鍵：
// 下一次重跑要重新送出退回的 chunk，不能整個命中快取（LLM-R8）。
func (p *Pipeline) assembleLLM(ctx context.Context, it store.Item, job *llmJob, rows []store.LLMChunk, stage *store.StageResult) (result, error) {
	if len(rows) != len(job.chunks) {
		return result{}, errPermanent(fmt.Sprintf("chunk 記錄有 %d 個，切分結果有 %d 個", len(rows), len(job.chunks)))
	}
	outputs := make([][]llm.Cue, len(rows))
	records := make([]chunkRecord, 0, len(rows))
	fallbacks := []fallbackRecord{}
	var candidates []string
	for k, row := range rows {
		c := job.chunks[k]
		if row.Chunk != c.Index {
			return result{}, errPermanent(fmt.Sprintf("chunk 記錄的順序不正確（第 %d 個是 %d）", k+1, row.Chunk))
		}
		in := job.chunkInput(c)
		switch row.Status {
		case store.ChunkDone:
			cues, err := llm.DecodeCues(row.Output)
			if err != nil {
				return result{}, errPermanent(fmt.Sprintf("chunk %d 的輸出不正確：%v", c.Index, err))
			}
			outputs[k] = cues
			if job.def.terms != nil {
				// 輸出已通過 schema 驗證；解不出來就當成沒有候選，不讓它影響字幕的產出。
				if ts, err := job.def.terms(row.Output); err == nil {
					candidates = append(candidates, ts...)
				}
			}
		case store.ChunkFallback:
			outputs[k] = job.def.fallback(in)
			fb := fallbackRecord{Chunk: c.Index, Reason: row.Reason}
			if len(row.Detail) > 2 { // 不是 {}
				_ = json.Unmarshal(row.Detail, &fb.Detail)
			}
			fallbacks = append(fallbacks, fb)
		default:
			return result{}, errPermanent(fmt.Sprintf("chunk %d 還沒有結果", c.Index))
		}
		rec := chunkRecord{Chunk: c.Index, Cues: [2]int{c.From, c.To}, Reused: row.Reused}
		if c.HasContext() {
			rec.Context = &[2]int{c.ContextFrom, c.ContextTo}
		}
		if !row.Reused && len(row.Usage) > 0 {
			_ = json.Unmarshal(row.Usage, &rec.Usage)
		}
		records = append(records, rec)
	}
	merged, err := llm.MergeCues(job.chunks, outputs)
	if err != nil {
		return result{}, errPermanent("合併 chunk 的輸出失敗：" + err.Error())
	}
	out, err := job.def.apply(job.tr, merged)
	if err != nil {
		return result{}, errPermanent("套用輸出失敗：" + err.Error())
	}
	out.Variant = job.def.variant

	prov, err := p.provenance(map[string]any{
		"stage": job.stage, "impl_version": p.implVersion(job.stage),
		"executor": ExecutorAPI, "provider": job.cfg.Provider, "model": job.cfg.Model,
		"prompt":        map[string]any{"id": job.prompt.ID, "version": job.prompt.Version, "sha256": job.prompt.SHA256},
		"params_sha256": job.paramsSHA,
		"chunks":        records,
		"fallbacks":     fallbacks,
		"derived_from":  []map[string]string{{"artifact": job.input.ID, "variant": job.input.Variant, "sha256": job.input.SHA256}},
	}, nil)
	if err != nil {
		return result{}, err
	}
	art, err := p.writeCuesArtifact(ctx, it.ID, out, []string{job.input.ID}, prov)
	if err != nil {
		return result{}, err
	}
	if len(fallbacks) > 0 {
		stage.CacheKey = ""
	}
	state := store.StageState{Status: store.StatusDone}
	if job.def.notes != nil {
		state.Message, state.Actions = job.def.notes(fallbacks)
	}
	res := result{task: taskDone, advance: true, stage: stage, artifact: art, state: state}
	if len(candidates) > 0 && it.ChannelID != "" {
		res.inTx = func(ctx context.Context, tx *store.Repo) (bool, error) {
			return false, addTermCandidates(ctx, tx, it, job, candidates)
		}
	}
	return res, nil
}

// addTermCandidates 把模型提出的術語候選寫入頻道（COR-R6）：狀態是 pending，不會自動加入詞彙表。
// 清理（控制字元、長度、數量）之後，已經在詞彙表中的詞略過；頻道已有的詞（任何狀態，包含已被拒絕的）也不重複；
// 一個頻道累積的候選有上限。
func addTermCandidates(ctx context.Context, tx *store.Repo, it store.Item, job *llmJob, raw []string) error {
	n, err := tx.CountTermCandidates(ctx, it.ChannelID)
	if err != nil {
		return err
	}
	for _, term := range llm.SanitizeTermCandidates(raw, llm.MaxTermCandidatesPerChunk*len(job.chunks)) {
		if n >= maxTermCandidatesPerChannel {
			return nil
		}
		if job.glossarySet[strings.ToLower(term)] {
			continue
		}
		added, err := tx.AddTermCandidate(ctx, it.ChannelID, it.ID, term)
		if err != nil {
			return err
		}
		if added {
			n++
		}
	}
	return nil
}
